package service

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
	"github.com/xtls/xray-core/infra/conf"
	"x-ui/database/model"
)

// Stored only in the actor-owned, restore-revoked interaction session.
type telegramInboundDraft struct {
	protocol                             model.Protocol
	step                                 int
	remark, domain, email                string
	port, certificateInbound, monthlyDay int
	total, expiry                        int64
	clientID, subID                      string
}

var inboundDomain = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$`)
var inboundEmail = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)
var inboundPrompts = []string{
	"回复此消息：入站备注（1–128字符）", "回复此消息：监听端口（1–65535）", "回复此消息：TLS 域名（不含协议或路径）",
	"回复此消息：已有 TLS 证书所在入站 ID（只复用 certificateFile/keyFile 引用，不签发证书）", "回复此消息：首客户端标识（字母、数字、_.@-）",
	"回复此消息：额度 GB（非负整数，0 不限）", "回复此消息：到期日期 YYYY-MM-DD（UTC，0 不限）", "回复此消息：月重置日（1–31，0 关闭）",
}

func inboundKeyboard(actions ...string) *telego.InlineKeyboardMarkup {
	k := &telego.InlineKeyboardMarkup{}
	labels := map[string]string{"add_inbound_vless": "VLESS + WebSocket + TLS", "add_inbound_vmess": "VMess + WebSocket + TLS", "add_inbound_confirm": "确认创建", "add_inbound_edit": "修正 / 重新填写", "add_inbound_cancel": "取消"}
	for _, a := range actions {
		k.InlineKeyboard = append(k.InlineKeyboard, []telego.InlineKeyboardButton{{Text: labels[a], CallbackData: a}})
	}
	return k
}
func (t *Tgbot) inboundPrompt(actor int64, prefix string) {
	d := wizardSessions[actor].inbound
	if d.step < len(inboundPrompts) {
		t.SendMsgToTgbot(actor, prefix+inboundPrompts[d.step], inboundKeyboard("add_inbound_cancel"))
		return
	}
	// Explicit allowlist: never serialize the draft, settings, UUID or certificate key.
	preview := fmt.Sprintf("创建入站预览\n备注：%s\n模板：%s + WebSocket + TLS\n监听端口：%d\n域名：%s\n证书引用入站：%d\n首客户端：%s\n额度：%d GB\n到期 UTC：%s\n月重置日：%d（0关闭）\n确认后一次提交；不签发证书、不修改防火墙。", html.EscapeString(d.remark), d.protocol, d.port, html.EscapeString(d.domain), d.certificateInbound, html.EscapeString(d.email), d.total/(1024*1024*1024), inboundExpiryLabel(d.expiry), d.monthlyDay)
	t.SendMsgToTgbot(actor, prefix+preview, inboundKeyboard("add_inbound_confirm", "add_inbound_edit", "add_inbound_cancel"))
}
func inboundExpiryLabel(ms int64) string {
	if ms == 0 {
		return "不限"
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}
func (t *Tgbot) handleInboundText(message telego.Message) error {
	session := wizardSessions[message.From.ID]
	d := session.inbound
	if !time.Now().Before(session.until) {
		delete(wizardSessions, message.From.ID)
		return nil
	}
	// Text input must reply to the current actor-bound prompt, not an old menu.
	if message.ReplyToMessage == nil || !session.messages[message.ReplyToMessage.MessageID] || d.step >= len(inboundPrompts) || d.protocol == "" {
		return nil
	}
	value := strings.TrimSpace(message.Text)
	var err error
	switch d.step {
	case 0:
		if value == "" || len([]rune(value)) > 128 {
			err = fmt.Errorf("备注无效")
		} else {
			d.remark = value
		}
	case 1:
		var port int
		port, err = strconv.Atoi(value)
		if err == nil && (port < 1 || port > 65535) {
			err = fmt.Errorf("端口无效")
		}
		if err == nil {
			var exists bool
			exists, err = t.inboundService.checkPortExist("", port, 0)
			if exists {
				err = fmt.Errorf("端口已存在")
			}
			if err == nil {
				d.port = port
			}
		}
	case 2:
		if !inboundDomain.MatchString(value) || len(value) > 253 || net.ParseIP(value) != nil {
			err = fmt.Errorf("域名无效")
		} else {
			d.domain = value
		}
	case 3:
		var id int
		id, err = strconv.Atoi(value)
		if err == nil {
			_, err = t.inboundCertificate(id)
			if err == nil {
				d.certificateInbound = id
			}
		}
	case 4:
		if !inboundEmail.MatchString(value) {
			err = fmt.Errorf("客户端标识无效")
		} else {
			d.email = value
		}
	case 5:
		var gb int64
		gb, err = strconv.ParseInt(value, 10, 64)
		if err == nil && (gb < 0 || gb > 8589934591) {
			err = fmt.Errorf("额度无效")
		}
		if err == nil {
			d.total = gb * 1024 * 1024 * 1024
		}
	case 6:
		if value == "0" {
			d.expiry = 0
		} else {
			var expiry time.Time
			expiry, err = time.Parse("2006-01-02", value)
			if err == nil && !expiry.After(time.Now()) {
				err = fmt.Errorf("到期日期必须在未来")
			}
			if err == nil {
				d.expiry = expiry.UnixMilli()
			}
		}
	case 7:
		var day int
		day, err = strconv.Atoi(value)
		if err == nil && (day < 0 || day > 31) {
			err = fmt.Errorf("重置日无效")
		}
		if err == nil {
			d.monthlyDay = day
		}
	}
	if err != nil {
		t.inboundPrompt(message.From.ID, "输入无效，请修正。\n")
		return nil
	}
	d.step++
	session.until = time.Now().Add(20 * time.Minute)
	t.inboundPrompt(message.From.ID, "")
	return nil
}

// Reuse an existing file reference only; reject inline key material and verify
// the local pair before persistence without putting certificate contents in chat.
func (t *Tgbot) inboundCertificate(id int) (map[string]any, error) {
	in, err := t.inboundService.GetInbound(id)
	if err != nil {
		return nil, err
	}
	var stream struct {
		Security string `json:"security"`
		TLS      struct {
			Certificates []struct {
				CertificateFile string `json:"certificateFile"`
				KeyFile         string `json:"keyFile"`
			} `json:"certificates"`
		} `json:"tlsSettings"`
	}
	if err = json.Unmarshal([]byte(in.StreamSettings), &stream); err != nil {
		return nil, err
	}
	if stream.Security != "tls" || len(stream.TLS.Certificates) != 1 {
		return nil, fmt.Errorf("需要一个已有文件证书引用")
	}
	cert := stream.TLS.Certificates[0]
	if cert.CertificateFile == "" || cert.KeyFile == "" {
		return nil, fmt.Errorf("需要已有证书文件")
	}
	if _, err = tls.LoadX509KeyPair(cert.CertificateFile, cert.KeyFile); err != nil {
		return nil, fmt.Errorf("已有证书不可用")
	}
	return map[string]any{"certificateFile": cert.CertificateFile, "keyFile": cert.KeyFile}, nil
}
func (t *Tgbot) buildInbound(d *telegramInboundDraft) (*model.Inbound, error) {
	if _, err := uuid.Parse(d.clientID); err != nil {
		return nil, fmt.Errorf("客户端 UUID 无效")
	}
	if d.step != len(inboundPrompts) || (d.protocol != model.VLESS && d.protocol != model.VMESS) || d.port < 1 || d.port > 65535 || !inboundDomain.MatchString(d.domain) || !inboundEmail.MatchString(d.email) || d.monthlyDay < 0 || d.monthlyDay > 31 || d.total < 0 || (d.expiry != 0 && d.expiry <= time.Now().UnixMilli()) {
		return nil, fmt.Errorf("模板参数无效")
	}
	owner, err := (&UserService{}).GetFirstUser()
	if err != nil {
		return nil, err
	}
	cert, err := t.inboundCertificate(d.certificateInbound)
	if err != nil {
		return nil, err
	}
	pair, err := tls.LoadX509KeyPair(cert["certificateFile"].(string), cert["keyFile"].(string))
	if err != nil || len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("已有证书不可用")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("已有证书不可用")
	}
	now := time.Now()
	if leaf.VerifyHostname(d.domain) != nil || now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("证书域名或有效期不匹配")
	}
	client := model.Client{ID: d.clientID, SubID: d.subID, Email: d.email, TotalGB: d.total, ExpiryTime: d.expiry, Enable: true, MonthlyResetEnabled: d.monthlyDay > 0, MonthlyResetDay: d.monthlyDay}
	settings := map[string]any{"clients": []model.Client{client}}
	if d.protocol == model.VLESS {
		settings["decryption"] = "none"
	} // current VLESS editor default
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	stream, err := json.Marshal(map[string]any{"network": "ws", "security": "tls", "wsSettings": map[string]any{"path": "/", "host": d.domain}, "tlsSettings": map[string]any{"serverName": d.domain, "certificates": []any{cert}}})
	if err != nil {
		return nil, err
	}
	in := &model.Inbound{UserId: owner.Id, Remark: d.remark, Enable: true, Port: d.port, Protocol: d.protocol, Tag: fmt.Sprintf("inbound-%d", d.port), Settings: string(raw), StreamSettings: string(stream), Sniffing: "{}"}
	exists, err := t.inboundService.checkPortExist(in.Listen, in.Port, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("端口已存在")
	}
	config, err := json.Marshal(in.GenXrayInboundConfig())
	if err != nil {
		return nil, err
	}
	var core conf.InboundDetourConfig
	if err = json.Unmarshal(config, &core); err != nil {
		return nil, err
	}
	if _, err = core.Build(); err != nil {
		return nil, fmt.Errorf("生成配置无效")
	}
	return in, nil
}
func (t *Tgbot) handleInboundCallback(q *telego.CallbackQuery, action string) {
	actor := q.From.ID
	if action == "add_inbound" {
		delete(userStates, actor)
		delete(bindingRequests, actor)
		wizardSessions[actor] = &telegramWizard{until: time.Now().Add(20 * time.Minute), messages: map[int]bool{}, inbound: &telegramInboundDraft{clientID: uuid.NewString(), subID: t.randomLowerAndNum(16)}}
		t.SendMsgToTgbot(actor, "请选择入站模板（仅已有证书引用）", inboundKeyboard("add_inbound_vless", "add_inbound_vmess", "add_inbound_cancel"))
		return
	}
	session := wizardSessions[actor]
	d := session.inbound
	switch action {
	case "add_inbound_cancel":
		delete(wizardSessions, actor)
		t.SendMsgToTgbot(actor, "已取消添加入站")
	case "add_inbound_vless", "add_inbound_vmess":
		if d.protocol != "" {
			return
		}
		d.protocol = model.Protocol(strings.TrimPrefix(action, "add_inbound_"))
		t.inboundPrompt(actor, "")
	case "add_inbound_edit":
		d.step = 0
		t.inboundPrompt(actor, "")
	case "add_inbound_confirm":
		in, err := t.buildInbound(d)
		if err != nil {
			t.inboundPrompt(actor, "未提交：校验失败，请修正后重试。\n")
			return
		}
		_, needsReconcile, err := t.inboundService.AddInbound(in)
		if err != nil {
			t.inboundPrompt(actor, "未提交：数据库或身份校验失败，请修正后重试。\n")
			return
		}
		delete(wizardSessions, actor)
		text := fmt.Sprintf("已提交入站 #%d。需要连接链接 / QR 时请主动点击下方按钮；未发送连接秘密。", in.Id)
		if needsReconcile {
			if t.xrayService != nil {
				t.xrayService.SetToNeedRestart()
			}
			text += "\n核心应用失败 / 待重试：数据已保留，勿重复创建。请在现有面板检查并重试核心应用。"
		}
		t.SendMsgToTgbot(actor, text, inboundResultKeyboard(in, d.clientID))
	}
}
