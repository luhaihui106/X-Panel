package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"
	"x-ui/database/model"
	"x-ui/util/common"
)

func inboundResultKeyboard(in *model.Inbound, clientID string) *telego.InlineKeyboardMarkup {
	suffix := fmt.Sprintf(" %d %s", in.Id, clientID)
	return &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{
		{Text: "获取连接链接", CallbackData: "inbound_result_link" + suffix},
		{Text: "获取 QR", CallbackData: "inbound_result_qr" + suffix},
	}}}
}

// Called only after the issued private actor/message/TTL gate, under the restore lock.
// Re-read the committed target and client identity; never keep a secret-bearing draft.
func (t *Tgbot) handleInboundResult(q *telego.CallbackQuery, decoded string) {
	args := strings.Split(decoded, " ")
	id, err := strconv.Atoi(args[1])
	if err != nil || !checkAdmin(q.From.ID) || t.inboundService == nil {
		t.denyCallback(q)
		return
	}
	in, err := t.inboundService.GetInbound(id)
	if err != nil || !in.Enable || (in.Protocol != model.VLESS && in.Protocol != model.VMESS) {
		t.denyCallback(q)
		return
	}
	clients, err := t.inboundService.GetClients(in)
	if err != nil {
		t.denyCallback(q)
		return
	}
	var client *model.Client
	for i := range clients {
		if clients[i].ID == args[2] && clients[i].Enable {
			client = &clients[i]
			break
		}
	}
	if client == nil {
		t.denyCallback(q)
		return
	}
	var stream struct {
		Network  string `json:"network"`
		Security string `json:"security"`
		WS       struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"wsSettings"`
		TLS struct {
			ServerName string   `json:"serverName"`
			ALPN       []string `json:"alpn"`
			Settings   struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"settings"`
		} `json:"tlsSettings"`
		ExternalProxy []json.RawMessage `json:"externalProxy"`
	}
	if json.Unmarshal([]byte(in.StreamSettings), &stream) != nil || stream.Network != "ws" || stream.Security != "tls" || !inboundDomain.MatchString(stream.TLS.ServerName) || len(stream.ExternalProxy) > 0 {
		t.denyCallback(q)
		return
	}
	remarkModel, err := (&SettingService{}).GetRemarkModel()
	if err != nil || len(remarkModel) == 0 {
		t.denyCallback(q)
		return
	}
	orders := map[rune]string{'i': in.Remark, 'e': client.Email}
	var parts []string
	for _, c := range remarkModel[1:] {
		if orders[c] != "" {
			parts = append(parts, orders[c])
		}
	}
	remark := strings.Join(parts, remarkModel[:1])
	address := stream.TLS.ServerName // No public-IP lookup; the wizard's explicit TLS endpoint.
	var link string
	if in.Protocol == model.VMESS {
		payload := map[string]any{"v": "2", "ps": remark, "add": address, "port": in.Port, "id": client.ID, "scy": client.Security, "net": "ws", "type": "none", "tls": "tls", "path": stream.WS.Path, "host": stream.WS.Host, "sni": stream.TLS.ServerName}
		if len(stream.TLS.ALPN) > 0 {
			payload["alpn"] = strings.Join(stream.TLS.ALPN, ",")
		}
		if stream.TLS.Settings.Fingerprint != "" {
			payload["fp"] = stream.TLS.Settings.Fingerprint
		}
		link = common.VMessLink(payload)
	} else {
		var settings model.VLESSSettings
		if json.Unmarshal([]byte(in.Settings), &settings) != nil {
			t.denyCallback(q)
			return
		}
		encryption := settings.Encryption
		if encryption == "" {
			encryption = "none"
		}
		params := map[string]string{"type": "ws", "encryption": encryption, "security": "tls", "path": stream.WS.Path, "host": stream.WS.Host, "sni": stream.TLS.ServerName}
		if len(stream.TLS.ALPN) > 0 {
			params["alpn"] = strings.Join(stream.TLS.ALPN, ",")
		}
		if stream.TLS.Settings.Fingerprint != "" {
			params["fp"] = stream.TLS.Settings.Fingerprint
		}
		link = common.VLESSLink(client.ID, address, in.Port, params, remark)
	}
	if args[0] == "inbound_result_link" {
		t.SendMsgToTgbot(q.From.ID, "<code>"+html.EscapeString(link)+"</code>")
		return
	}
	// Same QR payload and 400px / Low recovery rule as the existing panel QR modal.
	png, err := qrcode.Encode(link, qrcode.Low, 400)
	if err != nil {
		t.SendMsgToTgbot(q.From.ID, "QR 生成失败，请重试。")
		return
	}
	_, err = bot.SendPhoto(context.Background(), &telego.SendPhotoParams{ChatID: tu.ID(q.From.ID), Photo: tu.FileFromReader(bytes.NewReader(png), "inbound-qr.png")})
	if err != nil {
		t.SendMsgToTgbot(q.From.ID, "QR 发送失败，请重试。")
	}
}
