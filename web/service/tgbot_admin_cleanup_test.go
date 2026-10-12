package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
	"path/filepath"
	"time"
	"x-ui/web/global"
	"x-ui/web/locale"
)

func TestBotMenuAndRetiredCallbacks(t *testing.T) {
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	files, _ := filepath.Glob("../translation/translate.en_US.toml")
	if len(files) == 0 {
		t.Fatal("real bot translations not found")
	}
	for _, file := range files {
		if _, err := bundle.LoadMessageFile(file); err != nil {
			t.Fatal(err)
		}
	}
	oldLocale := locale.LocalizerBot
	locale.LocalizerBot = i18n.NewLocalizer(bundle, "en-US")
	defer func() { locale.LocalizerBot = oldLocale }()
	var payloads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		payloads = append(payloads, string(body))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`)
	}))
	defer server.Close()
	oldBot, oldRunning, oldHash := bot, isRunning, hashStorage
	defer func() { bot, isRunning, hashStorage = oldBot, oldRunning, oldHash }()
	notifier := &Tgbot{}
	var err error
	bot, err = notifier.NewBot(strings.Repeat("1", 9)+":"+strings.Repeat("a", 35), "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	isRunning = true
	hashStorage = global.NewHashStorage(time.Minute)
	notifier.SendAnswer(42, "menu", true)
	if len(payloads) != 1 {
		t.Fatal("administrator menu missing")
	}
	var menu struct {
		ReplyMarkup struct {
			Rows [][]struct {
				Data string `json:"callback_data"`
			} `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	if err := json.Unmarshal([]byte(payloads[0]), &menu); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, row := range menu.ReplyMarkup.Rows {
		for _, button := range row {
			found[button.Data] = true
		}
	}
	for _, retired := range []string{"lottery_play_menu", "vps_recommend", "oneclick_options"} {
		if found[retired] {
			t.Fatalf("retired menu action remains: %s", retired)
		}
	}
	for _, retained := range []string{"get_usage", "restart_panel", "reset_all_traffics", "get_backup", "get_inbounds", "add_client", "subconverter_install"} {
		if !found[retained] {
			t.Fatalf("administrator action removed: %s", retained)
		}
	}
	for _, action := range []string{"lottery_play", "lottery_skip", "lottery_play_menu", "vps_recommend", "oneclick_options", "oneclick_reality", "oneclick_switch_vision"} {
		before := len(payloads)
		notifier.answerCallback(&telego.CallbackQuery{ID: "test", Data: action, From: telego.User{ID: 42}, Message: &telego.Message{MessageID: 1, Chat: telego.Chat{ID: 42}}}, true)
		if len(payloads) != before {
			t.Fatalf("retired callback still performs action: %s", action)
		}
	}
}
