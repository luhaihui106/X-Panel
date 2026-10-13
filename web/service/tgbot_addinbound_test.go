package service

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/mymmrac/telego"
	"strings"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
)

func stage4Text(t *testing.T, n *Tgbot, actor int64, text string) {
	t.Helper()
	prompt := stage3OutboundMessages[actor]
	n.handleTelegramMessage(telego.Message{From: &telego.User{ID: actor}, Chat: telego.Chat{ID: actor, Type: "private"}, Text: text, ReplyToMessage: &prompt})
}
func stage4Rendered(t *testing.T, actor int64, action string) *telego.CallbackQuery {
	t.Helper()
	msg := stage3OutboundMessages[actor]
	for _, row := range msg.ReplyMarkup.InlineKeyboard {
		for _, button := range row {
			if issuedCallbacks[button.CallbackData].data == action {
				return &telego.CallbackQuery{ID: uuid.NewString(), From: telego.User{ID: actor}, Data: button.CallbackData, Message: &msg}
			}
		}
	}
	t.Fatalf("no current action %s", action)
	return nil
}
func stage4Fill(t *testing.T, n *Tgbot, actor int64, protocol string, port int, email string) {
	t.Helper()
	n.answerCallback(securityQuery(actor, "add_inbound"), true)
	n.answerCallback(stage3Rendered(t, actor, "add_inbound_"+protocol), true)
	for _, text := range []string{fmt.Sprintf("remark%d", actor), fmt.Sprint(port), "example.com", "1", email, "2", "0", "15"} {
		stage4Text(t, n, actor, text)
	}
}
func TestStage4ActualWizardInterleaved(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	stage4CertificateFixture(t, s)
	oldP := p
	p = nil
	t.Cleanup(func() { p = oldP })
	for _, a := range []int64{42, 43} {
		n.answerCallback(securityQuery(a, "add_inbound"), true)
		protocol := "vless"
		if a == 43 {
			protocol = "vmess"
		}
		n.answerCallback(stage3Rendered(t, a, "add_inbound_"+protocol), true)
	}
	values := [][]string{{"one", "23101", "one.example.com", "1", "one", "1", "0", "0"}, {"two", "23102", "two.example.com", "1", "two", "3", "2030-01-01", "28"}}
	for step := 0; step < 8; step++ {
		for i, a := range []int64{43, 42} {
			stage4Text(t, n, a, values[1-i][step])
		}
	}
	for _, a := range []int64{42, 43} {
		q := stage3Rendered(t, a, "add_inbound_confirm")
		n.answerCallback(q, true)
		q.ID += "replay"
		n.answerCallback(q, true)
	}
	inbounds, e := s.GetAllInbounds()
	if e != nil || len(inbounds) != 3 {
		t.Fatalf("inbounds %d %v", len(inbounds), e)
	}
	for i, email := range []string{"one", "two"} {
		stat, c, e := s.GetClientByEmail(email)
		if e != nil || c == nil {
			t.Fatal(e)
		}
		in, e := s.GetInbound(stat.InboundId)
		if e != nil || in.Port != 23101+i || c.TotalGB != int64(1+2*i)*1024*1024*1024 || c.MonthlyResetEnabled != (i == 1) {
			t.Fatalf("mixed draft: %+v %+v %v", in, c, e)
		}
		if in.UserId == 0 || in.Tag != fmt.Sprintf("inbound-%d", in.Port) {
			t.Fatal("owner/tag")
		}
	}
	for _, texts := range stage3OutboundTexts {
		for _, text := range texts {
			if strings.Contains(text, "PRIVATE KEY") || strings.Contains(text, "vless://") || strings.Contains(text, "vmess://") {
				t.Fatal("unsolicited secret")
			}
		}
	}
}
func TestStage4DenialCancelTimeoutAndCorrection(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	stage4CertificateFixture(t, s)
	stage4Fill(t, n, 42, "vless", 23201, "four")
	q := stage3Rendered(t, 42, "add_inbound_confirm")
	for _, kind := range []string{"actor", "ordinary", "message", "expired"} {
		bad := *q
		msg := *q.Message.(*telego.Message)
		bad.Message = &msg
		bad.ID += kind
		switch kind {
		case "actor":
			bad.From.ID = 43
			msg.Chat.ID = 43
		case "ordinary":
			bad.From.ID = 7
			msg.Chat.ID = 7
		case "message":
			msg.MessageID++
		case "expired":
			r := issuedCallbacks[q.Data]
			r.until = time.Now().Add(-time.Second)
			issuedCallbacks[q.Data] = r
		}
		n.answerCallback(&bad, true)
		if kind == "expired" {
			r := issuedCallbacks[q.Data]
			r.until = time.Now().Add(time.Minute)
			issuedCallbacks[q.Data] = r
		}
	}
	var count int64
	database.GetDB().Model(&model.Inbound{}).Count(&count)
	if count != 1 {
		t.Fatal("denial wrote DB")
	}
	n.answerCallback(stage3Rendered(t, 42, "add_inbound_edit"), true)
	stage4Text(t, n, 42, "corrected")
	stage4Text(t, n, 42, "0")
	if wizardSessions[42].inbound.step != 1 {
		t.Fatal("invalid port advanced")
	}
	n.answerCallback(stage4Rendered(t, 42, "add_inbound_cancel"), true)
	n.answerCallback(q, true)
	if wizardSessions[42] != nil {
		t.Fatal("cancel retained")
	}
	n.answerCallback(securityQuery(43, "add_inbound"), true)
	wizardSessions[43].until = time.Now().Add(-time.Second)
	n.answerCallback(stage3Rendered(t, 43, "add_inbound_vless"), true)
	if wizardSessions[43].inbound.protocol != "" {
		t.Fatal("expired template accepted")
	}
}
