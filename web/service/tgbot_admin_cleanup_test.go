package service

import (
	"github.com/mymmrac/telego"
	"strings"
	"testing"
	"time"
)

func TestBotMenuAndRetiredCallbacks(t *testing.T) {
	n, methods := stage1FakeTelegram(t)
	// Payload-derived messages include the whole actual Telegram keyboard.
	root := func() {
		n.answerCommand(&telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "/start"}, 42, false)
	}
	root()
	groups := []string{"menu_status", "menu_clients", "menu_backup", "menu_system"}
	found := map[string]bool{}
	inspect := func(message telego.Message) {
		for _, row := range message.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				r, ok := issuedCallbacks[b.CallbackData]
				if !ok {
					t.Fatal("outbound button not issued")
				}
				found[r.data] = true
				if strings.Contains(r.data, "lottery") || strings.Contains(r.data, "vps_recommend") || strings.Contains(r.data, "oneclick") {
					t.Fatalf("retired menu entry %s", r.data)
				}
			}
		}
	}
	inspect(stage3OutboundMessages[42])
	for _, group := range groups {
		if !found[group] {
			t.Fatalf("missing root group %s", group)
		}
		msg := stage3OutboundMessages[42]
		var token string
		for _, row := range msg.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				if issuedCallbacks[b.CallbackData].data == group {
					token = b.CallbackData
				}
			}
		}
		if token == "" {
			t.Fatalf("no payload button %s", group)
		}
		n.answerCallback(&telego.CallbackQuery{ID: token, From: telego.User{ID: 42}, Data: token, Message: &msg}, false)
		inspect(stage3OutboundMessages[42])
		back := stage3OutboundMessages[42]
		var backToken string
		for _, row := range back.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				if issuedCallbacks[b.CallbackData].data == "menu_root" {
					backToken = b.CallbackData
				}
			}
		}
		if backToken == "" {
			t.Fatal("no return button")
		}
		n.answerCallback(&telego.CallbackQuery{ID: backToken, From: telego.User{ID: 42}, Data: backToken, Message: &back}, false)
	}
	for _, action := range []string{"get_usage", "restart_panel", "reset_all_traffics", "get_backup", "get_inbounds", "add_client", "subconverter_install", "inbounds", "onlines", "get_banlogs", "get_sorted_traffic_usage_report", "deplete_soon", "commands"} {
		if !found[action] {
			t.Fatalf("administrator action removed: %s", action)
		}
	}
	for _, action := range []string{"lottery_play", "lottery_skip", "lottery_play_menu", "vps_recommend", "oneclick_options", "oneclick_reality", "oneclick_switch_vision"} {
		*methods = nil
		q := securityQuery(42, action)
		q.ID = action + time.Now().String()
		n.answerCallback(q, true)
		if len(*methods) != 1 || (*methods)[0] != "answerCallbackQuery" {
			t.Fatalf("retired callback escaped: %s", action)
		}
	}
}
