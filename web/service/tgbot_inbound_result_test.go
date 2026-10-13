package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
	"github.com/skip2/go-qrcode"
	"x-ui/config"
	"x-ui/database"
	"x-ui/database/model"
)

func stage4ResultQuery(t *testing.T, actor int64, action string) *telego.CallbackQuery {
	t.Helper()
	msg := stage3OutboundMessages[actor]
	for _, row := range msg.ReplyMarkup.InlineKeyboard {
		for _, button := range row {
			if strings.HasPrefix(issuedCallbacks[button.CallbackData].data, action+" ") {
				return &telego.CallbackQuery{ID: uuid.NewString(), From: telego.User{ID: actor}, Data: button.CallbackData, Message: &msg}
			}
		}
	}
	t.Fatalf("no result action %s", action)
	return nil
}

func TestStage4RequestedConnectionPayloadAndQR(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess"} {
		t.Run(protocol, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, _ := stage1FakeTelegram(t)
			n.inboundService = s
			stage4CertificateFixture(t, s)
			h := commitCore(t, false)
			stage4Fill(t, n, 42, protocol, 25001, "result-client")
			clientID := wizardSessions[42].inbound.clientID
			n.answerCallback(stage4Rendered(t, 42, "add_inbound_confirm"), true)
			linkQ := stage4ResultQuery(t, 42, "inbound_result_link")
			qrQ := stage4ResultQuery(t, 42, "inbound_result_qr")
			for _, text := range stage3OutboundTexts[42] {
				if strings.Contains(text, clientID) || strings.Contains(text, "://") {
					t.Fatal("unsolicited secret")
				}
			}
			// Change the persisted endpoint after commit: requests must read current DB, not the old draft.
			stat, _, _ := s.GetClientByEmail("result-client")
			if err := database.GetDB().Model(&model.Inbound{}).Where("id=?", stat.InboundId).Update("port", 25002).Error; err != nil {
				t.Fatal(err)
			}
			n.answerCallback(linkQ, true)
			texts := stage3OutboundTexts[42]
			output := texts[len(texts)-1]
			link := html.UnescapeString(strings.TrimSuffix(strings.TrimPrefix(output, "<code>"), "</code>"))
			if protocol == "vless" {
				u, e := url.Parse(link)
				if e != nil || u.Scheme != "vless" || u.User.Username() != clientID || u.Host != "example.com:25002" || u.Query().Get("path") != "/" || u.Query().Get("host") != "example.com" || u.Query().Get("sni") != "example.com" || u.Query().Get("security") != "tls" || u.Query().Get("type") != "ws" || u.Query().Get("encryption") != "none" || u.Fragment != "remark42-result-client" {
					t.Fatalf("bad VLESS payload %q %v", link, e)
				}
			} else {
				raw, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
				if e != nil {
					t.Fatal(e)
				}
				var p map[string]any
				if json.Unmarshal(raw, &p) != nil || p["id"] != clientID || p["add"] != "example.com" || p["port"] != float64(25002) || p["net"] != "ws" || p["tls"] != "tls" || p["host"] != "example.com" || p["path"] != "/" || p["sni"] != "example.com" || p["ps"] != "remark42-result-client" {
					t.Fatalf("bad VMess payload %s", raw)
				}
			}
			// Exercise actual multipart Telegram upload, not a dry QR adapter.
			photos := make(chan []byte, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/sendPhoto") {
					t.Errorf("unexpected request %s", r.URL.Path)
					return
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("chat_id") != "42" {
					t.Error("wrong QR recipient")
				}
				f, _, e := r.FormFile("photo")
				if e != nil {
					t.Error(e)
					return
				}
				defer f.Close()
				raw, _ := io.ReadAll(f)
				photos <- raw
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"ok":true,"result":{"message_id":900,"date":1,"chat":{"id":42,"type":"private"}}}`)
			}))
			defer srv.Close()
			old := bot
			b, e := n.NewBot(strings.Repeat("1", 9)+":"+strings.Repeat("a", 35), "", srv.URL)
			if e != nil {
				t.Fatal(e)
			}
			bot = b
			n.answerCallback(qrQ, true)
			bot = old
			select {
			case png := <-photos:
				want, _ := qrcode.Encode(link, qrcode.Low, 400)
				if !bytes.Equal(png, want) {
					t.Fatal("QR image does not encode the current exact connection payload with panel rules")
				}
			case <-time.After(time.Second):
				t.Fatal("no QR upload")
			}
			if h.calls.Load() != 1 {
				t.Fatal("read action changed core")
			}
		})
	}
}

func TestStage4RequestedConnectionDenials(t *testing.T) {
	for _, kind := range []string{"actor", "ordinary", "group", "message", "expired", "admin-revoked", "deleted-inbound", "deleted-client", "deleted-message", "restore"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, methods := stage1FakeTelegram(t)
			n.inboundService = s
			stage4CertificateFixture(t, s)
			h := commitCore(t, false)
			stage4Fill(t, n, 42, "vless", 25101, "deny-result")
			n.answerCallback(stage4Rendered(t, 42, "add_inbound_confirm"), true)
			queries := []*telego.CallbackQuery{stage4ResultQuery(t, 42, "inbound_result_link"), stage4ResultQuery(t, 42, "inbound_result_qr")}
			stat, _, _ := s.GetClientByEmail("deny-result")
			switch kind {
			case "admin-revoked":
				adminIds = []int64{43}
			case "deleted-inbound":
				database.GetDB().Delete(&model.Inbound{}, stat.InboundId)
			case "deleted-client":
				database.GetDB().Model(&model.Inbound{}).Where("id=?", stat.InboundId).Update("settings", `{"clients":[]}`)
			case "deleted-message":
				n.deleteMessageTgBot(42, queries[0].Message.GetMessageID())
			case "restore":
				stage4RestoreResultDB(t)
			}
			before := len(stage3OutboundTexts[42])
			beforeMethods := len(*methods)
			for _, q := range queries {
				msg := *q.Message.(*telego.Message)
				q.Message = &msg
				switch kind {
				case "actor":
					q.From.ID = 43
					msg.Chat.ID = 43
				case "ordinary":
					q.From.ID = 7
					msg.Chat.ID = 7
				case "group":
					msg.Chat.Type = "group"
				case "message":
					msg.MessageID++
				case "expired":
					r := issuedCallbacks[q.Data]
					r.until = time.Now().Add(-time.Second)
					issuedCallbacks[q.Data] = r
				}
				n.answerCallback(q, true)
			}
			if len(stage3OutboundTexts[42]) != before {
				t.Fatal("denial sent payload")
			}
			for _, method := range (*methods)[beforeMethods:] {
				if method == "sendPhoto" || method == "sendMessage" {
					t.Fatal("denial sent secret")
				}
			}
			if h.calls.Load() != 1 {
				t.Fatal("denial changed core")
			}
		})
	}
}

func stage4RestoreResultDB(t *testing.T) {
	t.Helper()
	path := config.GetDBPath()
	backup := filepath.Join(t.TempDir(), "restore.db")
	if e := database.GetDB().Exec("VACUUM INTO ?", backup).Error; e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(backup)
	if e != nil {
		t.Fatal(e)
	}
	if e = database.CloseDB(); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = database.InitDB(path); e != nil {
		t.Fatal(e)
	}
	server := &ServerService{importCoreEffects: &databaseImportCoreEffects{stop: func() {}, restart: func() error { return nil }}}
	f, e := os.Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = server.ImportDB(f); e != nil {
		t.Fatal(fmt.Errorf("restore: %w", e))
	}
}
