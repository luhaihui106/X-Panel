package service

import (
	"github.com/mymmrac/telego"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoginNotificationDoesNotHandlePasswords(t *testing.T) {
	source, err := os.ReadFile("tgbot.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "func (t *Tgbot) UserLoginNotify(")
	end := strings.Index(string(source)[start:], "\nfunc ")
	if strings.Contains(string(source)[start:start+end], "password") || strings.Contains(string(source)[start:start+end], "Password==") {
		t.Fatal("notification still accepts or emits password")
	}
	controller, err := os.ReadFile("../controller/index.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(controller), "safePass") || strings.Contains(string(controller), "password: ") {
		t.Fatal("failed login still logs or passes password")
	}
}

func TestCallbackRejectsUnprivilegedBackup(t *testing.T) {
	oldBot, oldAdmins := bot, adminIds
	defer func() { bot, adminIds = oldBot, oldAdmins }()
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer srv.Close()
	notifier := &Tgbot{}
	var err error
	bot, err = notifier.NewBot(strings.Repeat("1", 9)+":"+strings.Repeat("a", 35), "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	adminIds = []int64{42}
	q := &telego.CallbackQuery{ID: "deny", From: telego.User{ID: 7}, Data: "get_backup", Message: &telego.Message{MessageID: 1, Date: time.Now().Unix(), Chat: telego.Chat{ID: 7, Type: "private"}, ReplyMarkup: &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{{Text: "backup", CallbackData: "get_backup"}}}}}}
	func() {
		defer func() {
			if recover() != nil {
				t.Error("unauthorized callback reached backup side effect")
			}
		}()
		notifier.answerCallback(q, true)
	}()
	for _, action := range []string{"get_backup", "reset_all_traffics", "reset_all_traffics_c", "confirm_sub_install", "restart_panel_confirm", "add_client", "add_client_submit_enable", "reset_traffic_c victim", "get_usage", "get_sorted_traffic_usage_report"} {
		q.ID = "deny-" + action
		markup := secureKeyboard(7, &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{{Text: "action", CallbackData: action}}}}).(*telego.InlineKeyboardMarkup)
		bindKeyboard(7, 1, markup)
		q.Data = markup.InlineKeyboard[0][0].CallbackData
		q.Message.(*telego.Message).ReplyMarkup = markup
		methods = nil
		notifier.answerCallback(q, true)
		if len(methods) != 1 || !strings.HasSuffix(methods[0], "answerCallbackQuery") {
			t.Fatalf("%s escaped deny: %v", action, methods)
		}
	}
	if len(methods) != 1 || !strings.HasSuffix(methods[0], "answerCallbackQuery") {
		t.Fatalf("expected deny only, got %v", methods)
	}
}
