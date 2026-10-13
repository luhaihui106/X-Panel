package service

import (
	"github.com/mymmrac/telego"
	"testing"
	"time"
)

func sharedMessage(actor int64, id int32, user int64) telego.Message {
	return telego.Message{From: &telego.User{ID: actor}, Chat: telego.Chat{ID: actor, Type: "private"}, UsersShared: &telego.UsersShared{RequestID: int(id), Users: []telego.SharedUser{{UserID: user}}}}
}

func TestUsersSharedActualHandlerLifecycle(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, methods := stage1FakeTelegram(t)
	n.inboundService = s
	n.xrayService = &XrayService{}
	assertBinding := func(want int64) {
		t.Helper()
		_, c, e := s.GetClientByEmail("review")
		if e != nil || c.TgID != want {
			t.Fatalf("binding want=%d client=%+v err=%v", want, c, e)
		}
	}
	outbound := func(actor int64) int32 {
		t.Helper()
		id, ok := stage1OutboundRequests[actor]
		if !ok || id >= 0 || id != bindingRequests[actor].id {
			t.Fatal("missing current outbound request or pending mismatch")
		}
		return id
	}
	n.clientTelegramUserInfo(42, "review")
	id := stage1OutboundRequests[42]
	if id != bindingRequests[42].id || id >= 0 {
		t.Fatal("outbound request ID differs from pending")
	}
	n.handleTelegramMessage(sharedMessage(7, id, 700))
	assertBinding(0)
	n.handleTelegramMessage(sharedMessage(43, id, 700))
	assertBinding(0)
	bad := sharedMessage(42, id, 700)
	bad.Chat.ID = 43
	n.handleTelegramMessage(bad)
	assertBinding(0)
	n.handleTelegramMessage(sharedMessage(42, id-1, 700))
	assertBinding(0)
	n.handleTelegramMessage(sharedMessage(42, id, 700))
	assertBinding(700)
	n.handleTelegramMessage(sharedMessage(42, id, 701))
	assertBinding(700)
	n.clientTelegramUserInfo(42, "review")
	id = outbound(42)
	*methods = nil
	n.handleCloseKeyboard(telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}})
	n.handleTelegramMessage(sharedMessage(42, id, 702))
	assertBinding(700)
	found := false
	for _, m := range *methods {
		if m == "remove_keyboard" {
			found = true
		}
	}
	if !found {
		t.Fatal("close did not remove reply keyboard")
	}
	n.clientTelegramUserInfo(42, "review")
	id = outbound(42)
	p := bindingRequests[42]
	p.until = time.Now().Add(-time.Second)
	bindingRequests[42] = p
	n.handleTelegramMessage(sharedMessage(42, id, 702))
	assertBinding(700)
	n.clientTelegramUserInfo(42, "review")
	a := outbound(42)
	n.clientTelegramUserInfo(43, "review")
	b := outbound(43)
	n.answerCallback(securityQuery(42, "tgid_remove_c review"), true)
	assertBinding(0)
	if len(bindingRequests) != 0 {
		t.Fatal("remove did not revoke both admins")
	}
	n.handleTelegramMessage(sharedMessage(42, a, 703))
	n.handleTelegramMessage(sharedMessage(43, b, 704))
	assertBinding(0)
	n.clientTelegramUserInfo(42, "review")
	a = outbound(42)
	n.clientTelegramUserInfo(43, "review")
	b = outbound(43)
	n.handleTelegramMessage(sharedMessage(42, b, 705))
	assertBinding(0)
	n.handleTelegramMessage(sharedMessage(43, a, 706))
	assertBinding(0)
	n.handleTelegramMessage(sharedMessage(42, a, 707))
	assertBinding(707)
	n.handleTelegramMessage(sharedMessage(43, b, 708))
	assertBinding(708)
	n.clientTelegramUserInfo(43, "review")
	b = stage1OutboundRequests[43]
	if b != bindingRequests[43].id {
		t.Fatal("second admin outbound ID mismatch")
	}
	n.answerCallback(securityQuery(43, "tgid_remove_c review"), true)
	assertBinding(0)
	n.handleTelegramMessage(sharedMessage(43, b, 709))
	assertBinding(0)
}
