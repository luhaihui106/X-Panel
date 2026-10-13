package service

import (
	"github.com/mymmrac/telego"
	"testing"
)

func TestStage3CloseRevokesOnlyOwnDraft(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	for _, a := range []int64{42, 43} {
		n.answerCallback(securityQuery(a, "add_client"), true)
		n.answerCallback(latestWizardQuery(t, a, "add_client_to 1"), true)
	}
	stale := latestWizardQuery(t, 42, "add_client_submit_enable")
	n.handleCloseKeyboard(telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}})
	if wizardSessions[42] != nil || wizardSessions[43] == nil {
		t.Error("close did not revoke only own session")
	}
	if _, ok := n.authorizeCallback(stale); ok {
		t.Error("closed draft button accepted")
	}
}
