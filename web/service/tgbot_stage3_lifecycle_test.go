package service

import (
	"github.com/mymmrac/telego"
	"strconv"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
)

func stage3Rendered(t *testing.T, actor int64, action string) *telego.CallbackQuery {
	t.Helper()
	callbackRegistryMu.Lock()
	defer callbackRegistryMu.Unlock()
	for token, r := range issuedCallbacks {
		if r.actor == actor && r.data == action && r.message > 0 {
			return &telego.CallbackQuery{ID: token, From: telego.User{ID: actor}, Data: token, Message: &telego.Message{MessageID: r.message, Date: time.Now().Unix(), Chat: telego.Chat{ID: actor, Type: "private"}, ReplyMarkup: &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{{Text: "rendered", CallbackData: token}}}}}}
		}
	}
	t.Fatalf("no rendered %s for %d", action, actor)
	return nil
}

func TestStage3InterleavedDraftsPersistDifferentParameters(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	oldP := p
	p = nil
	t.Cleanup(func() { p = oldP })
	for _, a := range []int64{42, 43} {
		n.answerCallback(securityQuery(a, "add_client"), true)
		n.answerCallback(latestWizardQuery(t, a, "add_client_to 1"), true)
	}
	for _, a := range []int64{42, 43} {
		n.answerCallback(latestWizardQuery(t, a, "add_client_ch_default_email"), true)
	}
	for _, a := range []int64{43, 42} {
		n.handleTelegramMessage(telego.Message{From: &telego.User{ID: a}, Chat: telego.Chat{ID: a, Type: "private"}, Text: "admin" + strconv.FormatInt(a, 10)})
	}
	for _, a := range []int64{42, 43} {
		n.answerCallback(latestWizardQuery(t, a, "add_client_ch_default_traffic"), true)
		amount := "1"
		if a == 43 {
			amount = "5"
		}
		n.answerCallback(latestWizardQuery(t, a, "add_client_limit_traffic_c "+amount), true)
	}
	stale := latestWizardQuery(t, 42, "add_client_submit_enable")
	for _, a := range []int64{43, 42} {
		n.answerCallback(latestWizardQuery(t, a, "add_client_submit_enable"), true)
	}
	n.answerCallback(stale, true)
	for _, a := range []int64{42, 43} {
		_, c, e := s.GetClientByEmail("admin" + strconv.FormatInt(a, 10))
		gb := int64(1)
		if a == 43 {
			gb = 5
		}
		if e != nil || c == nil || c.TotalGB != gb*1024*1024*1024 {
			t.Fatalf("mixed draft %d: %+v %v", a, c, e)
		}
	}
	inbound, _ := s.GetInbound(1)
	clients, _ := s.GetClients(inbound)
	if len(clients) != 3 {
		t.Fatal("duplicate submit")
	}
}

func TestStage3WizardTimeoutCancelIsolation(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	for _, a := range []int64{42, 43} {
		n.answerCallback(securityQuery(a, "add_client"), true)
		n.answerCallback(latestWizardQuery(t, a, "add_client_to 1"), true)
	}
	stale := latestWizardQuery(t, 42, "add_client_submit_enable")
	n.answerCallback(latestWizardQuery(t, 42, "add_client_cancel"), true)
	n.answerCallback(stale, true)
	if wizardSessions[42] != nil || wizardSessions[43] == nil {
		t.Fatal("cancel crossed owners")
	}
	n.answerCallback(latestWizardQuery(t, 43, "add_client_ch_default_email"), true)
	draft := wizardSessions[43].draft
	before := draft.client_Email
	wizardSessions[43].until = time.Now().Add(-time.Second)
	n.handleTelegramMessage(telego.Message{From: &telego.User{ID: 43}, Chat: telego.Chat{ID: 43, Type: "private"}, Text: "late"})
	n.answerCallback(latestWizardQuery(t, 43, "add_client_default_info"), true)
	if draft.client_Email != before {
		t.Fatal("timeout changed draft")
	}
	inbound, _ := s.GetInbound(1)
	clients, _ := s.GetClients(inbound)
	if len(clients) != 1 {
		t.Fatal("cancel/timeout mutated DB")
	}
}

func TestStage3GroupsUserDenialAndOwnMenu(t *testing.T) {
	n, methods := stage1FakeTelegram(t)
	groups := map[string][]string{"menu_status": {"get_usage", "inbounds", "onlines", "get_sorted_traffic_usage_report", "deplete_soon", "get_banlogs"}, "menu_clients": {"get_inbounds", "add_client", "reset_all_traffics"}, "menu_backup": {"get_backup"}, "menu_system": {"restart_panel", "subconverter_install", "commands"}}
	for group, actions := range groups {
		n.answerCommand(&telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "/start"}, 42, false)
		n.answerCallback(stage3Rendered(t, 42, group), true)
		for _, action := range actions {
			stage3Rendered(t, 42, action)
		}
		*methods = nil
		n.answerCallback(securityQuery(7, group), true)
		if len(*methods) != 1 || (*methods)[0] != "answerCallbackQuery" {
			t.Fatal("user reached admin group")
		}
	}
	n.answerCommand(&telego.Message{From: &telego.User{ID: 7}, Chat: telego.Chat{ID: 7, Type: "private"}, Text: "/start"}, 7, true)
	stage3Rendered(t, 7, "client_traffic")
	stage3Rendered(t, 7, "client_commands")
}

func TestStage3DangerousConfirmCancelReplayActualPaths(t *testing.T) {
	for _, action := range []string{"reset_traffic", "clear_ips", "tgid_remove", "reset_all_traffics", "restart_panel", "restart_core"} {
		for _, confirm := range []bool{false, true} {
			t.Run(action+strconv.FormatBool(confirm), func(t *testing.T) {
				s, _ := reviewDB(t, false, false)
				n, methods := stage1FakeTelegram(t)
				n.inboundService = s
				n.xrayService = &XrayService{}
				raw := `{"clients":[{"id":"review-id","email":"review","enable":true,"tgId":700}]}`
				if err := database.GetDB().Model(&model.Inbound{}).Where("id=1").Update("settings", raw).Error; err != nil {
					t.Fatal(err)
				}
				initialIPs := "[\"192.0.2.1\"]"
				if err := database.GetDB().Exec("INSERT INTO inbound_client_ips(client_email,ips) VALUES (?,?)", "review", initialIPs).Error; err != nil {
					t.Fatal(err)
				}
				restarts := 0
				n.effects = &telegramExternalEffects{restart: func(int64) { restarts++ }, restartCore: func(int64) { restarts++ }}
				start, yes, no := action+" review", action+"_c review", "client_cancel review"
				switch action {
				case "clear_ips":
					no = "ips_cancel review"
				case "tgid_remove":
					no = "tgid_cancel review"
				case "reset_all_traffics":
					start = action
					yes = action + "_c"
					no = action + "_cancel"
				case "restart_panel":
					start = action
					yes = action + "_confirm"
					no = action + "_cancel"
				case "restart_core":
					yes = action + "_confirm"
					no = action + "_cancel"
				}
				before := reviewStat(t)
				if action == "restart_core" {
					n.answerCommand(&telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "/restart"}, 42, true)
				} else {
					n.answerCallback(securityQuery(42, start), true)
				}
				pending := stage3Rendered(t, 42, yes)
				// Denials run actual dispatch, and must not consume the legitimate button.
				for _, kind := range []string{"other-admin", "wrong-message", "expired", "ordinary"} {
					bad := *pending
					msg := *pending.Message.(*telego.Message)
					bad.Message = &msg
					bad.ID += "-" + kind
					switch kind {
					case "other-admin":
						bad.From.ID = 43
						msg.Chat.ID = 43
					case "ordinary":
						bad.From.ID = 7
						msg.Chat.ID = 7
					case "wrong-message":
						msg.MessageID++
					case "expired":
						callbackRegistryMu.Lock()
						r := issuedCallbacks[pending.Data]
						r.until = time.Now().Add(-time.Second)
						issuedCallbacks[pending.Data] = r
						callbackRegistryMu.Unlock()
					}
					*methods = nil
					n.answerCallback(&bad, true)
					if len(*methods) != 1 || (*methods)[0] != "answerCallbackQuery" {
						t.Fatalf("%s reached mutation path: %v", kind, *methods)
					}
					if kind == "expired" {
						callbackRegistryMu.Lock()
						r := issuedCallbacks[pending.Data]
						r.until = time.Now().Add(time.Minute)
						issuedCallbacks[pending.Data] = r
						callbackRegistryMu.Unlock()
					}
				}
				if reviewStat(t).Up != before.Up || restarts != 0 {
					t.Fatal("preview mutated")
				}
				if confirm {
					n.answerCallback(pending, true)
				} else {
					n.answerCallback(stage3Rendered(t, 42, no), true)
				}
				after := reviewStat(t)
				n.answerCallback(pending, true)
				pending.ID += "duplicate"
				n.answerCallback(pending, true)
				if reviewStat(t).Up != after.Up {
					t.Fatal("replay mutated")
				}
				if action == "reset_traffic" || action == "reset_all_traffics" {
					if confirm && after.Up != 0 || !confirm && after.Up != before.Up {
						t.Fatal("reset confirmation incorrect")
					}
				}
				if action == "restart_panel" || action == "restart_core" {
					expected := 0
					if confirm {
						expected = 1
					}
					if restarts != expected {
						t.Fatal("restart replay/cancel")
					}
				}
				_, c, e := s.GetClientByEmail("review")
				if e != nil {
					t.Fatal(e)
				}
				if action == "tgid_remove" {
					expected := int64(700)
					if confirm {
						expected = 0
					}
					if c.TgID != expected {
						t.Fatal("binding deletion incorrect")
					}
				}
				if action == "clear_ips" {
					ips, err := s.GetInboundClientIps("review")
					if err != nil {
						t.Fatal(err)
					}
					expected := initialIPs
					if confirm {
						expected = ""
					}
					if ips != expected {
						t.Fatalf("IP clearing/cancel/replay mismatch: %q want %q", ips, expected)
					}
					// A replay must not clear newly collected records after either decision.
					if err := database.GetDB().Exec("UPDATE inbound_client_ips SET ips=? WHERE client_email=?", initialIPs, "review").Error; err != nil {
						t.Fatal(err)
					}
					pending.ID += "-new-delivery"
					n.answerCallback(pending, true)
					ips, err = s.GetInboundClientIps("review")
					if err != nil || ips != initialIPs {
						t.Fatal("replay cleared newly collected IPs")
					}
				}
			})
		}
	}
}
