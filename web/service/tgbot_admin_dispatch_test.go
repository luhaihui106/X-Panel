package service

import (
	"strings"
	"testing"
)

func TestAdminDangerousActualDispatchDryExternalEffects(t *testing.T) {
	for _, action := range []string{"get_backup", "subconverter_install", "confirm_sub_install", "restart_panel_confirm"} {
		t.Run(action, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, methods := stage1FakeTelegram(t)
			n.inboundService = s
			called := []string{}
			n.effects = &telegramExternalEffects{
				backup:       func(chat int64) { called = append(called, "get_backup") },
				checkInstall: func(chat int64) { called = append(called, "subconverter_install") },
				install:      func() error { called = append(called, "confirm_sub_install"); return nil },
				restart:      func(chat int64) { called = append(called, "restart_panel_confirm") },
			}
			q := securityQuery(7, action)
			n.answerCallback(q, true)
			if len(called) != 0 || len(*methods) != 1 || (*methods)[0] != "answerCallbackQuery" {
				t.Fatal("nonadmin escaped fail-closed dispatch")
			}
			*methods = nil
			q = securityQuery(42, action)
			n.answerCallback(q, false)
			if strings.Join(called, ",") != action {
				t.Fatalf("actual dispatch did not reach dry adapter: %v", called)
			}
			found := false
			for _, m := range *methods {
				if m == "answerCallbackQuery" {
					found = true
				}
			}
			if !found {
				t.Fatal("no real callback answer")
			}
			n.answerCallback(q, true)
			if len(called) != 1 {
				t.Fatal("replay executed external side effect")
			}
		})
	}
}
