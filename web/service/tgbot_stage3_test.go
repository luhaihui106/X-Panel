package service

import (
	"github.com/mymmrac/telego"
	"strings"
	"testing"
)

func TestStage3AdminMenuGroupsActualCommand(t *testing.T) {
	n, _ := stage1FakeTelegram(t)
	n.answerCommand(&telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "/start"}, 42, false)
	for _, action := range []string{"menu_status", "menu_clients", "menu_backup", "menu_system"} {
		found := false
		for _, r := range issuedCallbacks {
			if r.actor == 42 && r.data == action && r.message > 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("missing rendered administrator group %s", action)
		}
	}
}

func TestStage3TwoAdministratorsStartActualWizard(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	n.answerCallback(securityQuery(42, "add_client"), true)
	n.answerCallback(securityQuery(43, "add_client"), true)
	for _, actor := range []int64{42, 43} {
		found := false
		for _, r := range issuedCallbacks {
			if r.actor == actor && r.data == "add_client_to 1" && r.message > 9 {
				found = true
			}
		}
		if !found {
			t.Errorf("administrator %d cannot independently select inbound", actor)
		}
	}
}

func TestStage3RestartCommandRequiresPreview(t *testing.T) {
	n, _ := stage1FakeTelegram(t)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("restart command reached core before confirmation: %v", r)
		}
	}()
	n.answerCommand(&telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "/restart"}, 42, true)
	found := false
	for _, r := range issuedCallbacks {
		if r.actor == 42 && r.data == "restart_core_confirm" {
			found = true
		}
	}
	if !found {
		t.Fatal("no core restart preview confirmation")
	}
}

func TestStage3DangerousPreviewsDescribeTarget(t *testing.T) {
	for _, action := range []string{"reset_traffic review", "clear_ips review", "tgid_remove review", "reset_all_traffics"} {
		t.Run(action, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, _ := stage1FakeTelegram(t)
			n.inboundService = s
			n.answerCallback(securityQuery(42, action), true)
			text := strings.Join(stage3OutboundTexts[42], "\n")
			if !strings.Contains(text, "预览") || (!strings.Contains(text, "review") && !strings.Contains(text, "全部")) {
				t.Fatalf("missing explicit target preview: %q", text)
			}
		})
	}
}
