package service

import (
	"github.com/mymmrac/telego"
	"testing"
	"time"
)

// Use the actual newly rendered callback token/message, not a forged gate fixture.
func latestWizardQuery(t *testing.T, actor int64, action string) *telego.CallbackQuery {
	t.Helper()
	callbackRegistryMu.Lock()
	defer callbackRegistryMu.Unlock()
	for token, r := range issuedCallbacks {
		if r.actor == actor && r.data == action && wizardMessages[r.message] {
			markup := &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{{Text: "actual rendered button", CallbackData: token}}}}
			return &telego.CallbackQuery{ID: token, From: telego.User{ID: actor}, Data: token, Message: &telego.Message{MessageID: r.message, Date: time.Now().Unix(), Chat: telego.Chat{ID: actor, Type: "private"}, ReplyMarkup: markup}}
		}
	}
	t.Fatalf("no live rendered wizard button: %s", action)
	return nil
}

func TestWizardDuplicateEmailCorrectionRetryActualHandler(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	n.xrayService = &XrayService{}
	oldP := p
	p = nil
	t.Cleanup(func() { p = oldP; wizardOwner = 0; wizardMessages = map[int]bool{}; userStates = map[int64]string{} })
	wizardOwner = 42
	wizardUntil = time.Now().Add(time.Minute)
	receiver_inbound_ID = 1
	client_Id = "new-id"
	client_Email = "review"
	client_TgID = "0"
	client_SubID = "retry-sub"
	client_Comment = ""
	client_TotalGB = 0
	client_ExpiryTime = 0
	client_LimitIP = 0
	client_Reset = 0
	text, err := n.BuildInboundClientDataMessage("review", "vless")
	if err != nil {
		t.Fatal(err)
	}
	n.addClient(42, text)
	first := latestWizardQuery(t, 42, "add_client_submit_enable")
	n.answerCallback(first, true)
	if wizardOwner != 42 {
		t.Fatal("failure released wizard owner")
	}
	retry := latestWizardQuery(t, 42, "add_client_submit_enable")
	if retry.Data == first.Data {
		t.Fatal("failure did not redraw fresh retry")
	}
	inbound, _ := s.GetInbound(1)
	clients, _ := s.GetClients(inbound)
	if len(clients) != 1 {
		t.Fatal("duplicate submission mutated clients")
	}
	n.answerCallback(first, true)
	n.answerCallback(latestWizardQuery(t, 42, "add_client_ch_default_email"), true)
	if userStates[42] != "awaiting_email" {
		t.Fatal("email correction state missing")
	}
	n.handleTelegramMessage(telego.Message{From: &telego.User{ID: 43}, Chat: telego.Chat{ID: 43, Type: "private"}, Text: "other-admin"})
	if client_Email != "review" {
		t.Fatal("other admin changed draft")
	}
	n.handleTelegramMessage(telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "corrected"})
	if client_Email != "corrected" {
		t.Fatal("actual message handler did not correct draft")
	}
	n.answerCallback(latestWizardQuery(t, 42, "add_client_submit_enable"), true)
	_, c, err := s.GetClientByEmail("corrected")
	if err != nil || c == nil || !c.Enable {
		t.Fatalf("retry not persisted: %v %+v", err, c)
	}
	inbound, _ = s.GetInbound(1)
	clients, _ = s.GetClients(inbound)
	if len(clients) != 2 || wizardOwner != 0 {
		t.Fatal("retry lifecycle incomplete")
	}
	n.answerCallback(retry, true)
	inbound, _ = s.GetInbound(1)
	clients, _ = s.GetClients(inbound)
	if len(clients) != 2 {
		t.Fatal("late retry duplicated client")
	}
}
