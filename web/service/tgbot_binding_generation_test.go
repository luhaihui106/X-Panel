package service

import (
	"database/sql"
	"encoding/json"
	"github.com/mymmrac/telego"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"x-ui/config"
	"x-ui/database"
	"x-ui/web/global"
	"x-ui/web/locale"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
	"os"
)

var stage1OutboundRequests = map[int64]int32{}
var stage3OutboundTexts = map[int64][]string{}
var stage3OutboundMessages = map[int64]telego.Message{}

// Only the loopback API is used; no Telegram credentials or network service.
func stage1FakeTelegram(t *testing.T) (*Tgbot, *[]string) {
	t.Helper()
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	oldOutbound := stage1OutboundRequests
	oldTexts := stage3OutboundTexts
	oldMessages := stage3OutboundMessages
	stage3OutboundMessages = map[int64]telego.Message{}
	t.Cleanup(func() { stage3OutboundMessages = oldMessages })
	stage3OutboundTexts = map[int64][]string{}
	t.Cleanup(func() { stage3OutboundTexts = oldTexts })
	stage1OutboundRequests = make(map[int64]int32)
	t.Cleanup(func() { stage1OutboundRequests = oldOutbound })
	oldIssued, oldConsumed := issuedCallbacks, consumedCallbacks
	oldSessions := wizardSessions
	wizardSessions = make(map[int64]*telegramWizard)
	t.Cleanup(func() { wizardSessions = oldSessions })
	issuedCallbacks = make(map[string]issuedCallback)
	consumedCallbacks = make(map[string]time.Time)
	t.Cleanup(func() { issuedCallbacks, consumedCallbacks = oldIssued, oldConsumed })
	oldLocale, oldHash := locale.LocalizerBot, hashStorage
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	data, err := os.ReadFile("../translation/translate.en_US.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bundle.ParseMessageFileBytes(data, "en_US.toml"); err != nil {
		t.Fatal(err)
	}
	locale.LocalizerBot = i18n.NewLocalizer(bundle, "en-US")
	hashStorage = global.NewHashStorage(time.Hour)
	t.Cleanup(func() { locale.LocalizerBot = oldLocale; hashStorage = oldHash })
	oldBot, oldAdmins, oldRunning := bot, adminIds, isRunning
	var mu sync.Mutex
	methods := []string{}
	id := 100
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		methods = append(methods, method)
		w.Header().Set("Content-Type", "application/json")
		if method == "sendMessage" || method == "editMessageText" {
			var payload struct {
				ChatID      int64           `json:"chat_id"`
				Text        string          `json:"text"`
				ReplyMarkup json.RawMessage `json:"reply_markup"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			stage3OutboundTexts[payload.ChatID] = append(stage3OutboundTexts[payload.ChatID], payload.Text)
			if strings.Contains(string(payload.ReplyMarkup), "request_users") {
				methods = append(methods, "request_users")
				var keyboard struct {
					Keyboard [][]struct {
						RequestUsers *struct {
							RequestID int32 `json:"request_id"`
						} `json:"request_users"`
					} `json:"keyboard"`
				}
				if err := json.Unmarshal(payload.ReplyMarkup, &keyboard); err != nil {
					t.Error(err)
				}
				for _, row := range keyboard.Keyboard {
					for _, button := range row {
						if button.RequestUsers != nil {
							stage1OutboundRequests[payload.ChatID] = button.RequestUsers.RequestID
						}
					}
				}
			}
			if strings.Contains(string(payload.ReplyMarkup), "remove_keyboard") {
				methods = append(methods, "remove_keyboard")
			}
			id++
			var markup telego.InlineKeyboardMarkup
			if err := json.Unmarshal(payload.ReplyMarkup, &markup); err == nil && len(markup.InlineKeyboard) > 0 {
				stage3OutboundMessages[payload.ChatID] = telego.Message{MessageID: id, Date: time.Now().Unix(), Chat: telego.Chat{ID: payload.ChatID, Type: "private"}, Text: payload.Text, ReplyMarkup: &markup}
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": id, "date": time.Now().Unix(), "chat": map[string]any{"id": payload.ChatID, "type": "private"}, "reply_markup": payload.ReplyMarkup}})
		} else {
			io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	notifier := &Tgbot{}
	bot, err = notifier.NewBot(strings.Repeat("1", 9)+":"+strings.Repeat("a", 35), "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	adminIds, isRunning = []int64{42, 43}, true
	t.Cleanup(func() {
		notifier.deferredDeletes.Wait()
		srv.Close()
		bot, adminIds, isRunning = oldBot, oldAdmins, oldRunning
		bindingRequests = make(map[int64]struct {
			id     int32
			client int
			until  time.Time
		})
	})
	return notifier, &methods
}

func TestBindingGenerationSurvivesDatabaseReopen(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	notifier, _ := stage1FakeTelegram(t)
	notifier.inboundService = s
	notifier.clientTelegramUserInfo(42, "review")
	first := bindingRequests[42].id
	if first >= 0 {
		t.Fatalf("legacy positive request namespace reused: %d", first)
	}
	sqlDB, err := database.GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Seq  int
		Name string
		File string
	}
	if err = database.GetDB().Raw("PRAGMA database_list").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	path := rows[0].File
	if !filepath.IsAbs(path) {
		t.Fatal("expected isolated database path")
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	bindingRequests = make(map[int64]struct {
		id     int32
		client int
		until  time.Time
	})
	if err = database.InitDB(path); err != nil {
		t.Fatal(err)
	}
	notifier.clientTelegramUserInfo(42, "review")
	second := bindingRequests[42].id
	if second >= first {
		t.Fatalf("request generation reused after restart: first=%d second=%d", first, second)
	}
}

func TestBindingGenerationStorageFailureFailsClosed(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	notifier, methods := stage1FakeTelegram(t)
	notifier.inboundService = s
	notifier.clientTelegramUserInfo(42, "review")
	if _, ok := bindingRequests[42]; !ok {
		t.Fatal("missing existing pending")
	}
	ledger, err := sql.Open("sqlite3", filepath.Join(config.GetDBFolderPath(), "telegram-binding-issuance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := ledger.Exec(`CREATE TRIGGER reject_binding BEFORE UPDATE ON issuance BEGIN SELECT RAISE(FAIL, 'injected reservation failure'); END`); err != nil {
		t.Fatal(err)
	}
	*methods = nil
	notifier.clientTelegramUserInfo(42, "review")
	if _, ok := bindingRequests[42]; ok {
		t.Fatalf("binding request escaped failed durable reservation; API calls=%v", *methods)
	}
	if strings.Contains(strings.Join(*methods, ","), "request_users") || !strings.Contains(strings.Join(*methods, ","), "remove_keyboard") {
		t.Fatalf("failed reservation keyboard: %v", *methods)
	}
}

func TestBindingGenerationExhaustionFailsClosed(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	notifier, _ := stage1FakeTelegram(t)
	notifier.inboundService = s
	if err := database.GetDB().Exec("INSERT INTO telegram_binding_sequences(id) VALUES (?)", int64(2147483647)).Error; err != nil {
		t.Fatal(err)
	}
	notifier.clientTelegramUserInfo(42, "review")
	if bindingRequests[42].id != -2147483648 {
		t.Fatal("last signed request ID unavailable")
	}
	notifier.clientTelegramUserInfo(42, "review")
	if _, ok := bindingRequests[42]; ok {
		t.Fatal("namespace exhaustion must revoke pending and fail closed")
	}
}

func TestStage1AllOrdinaryDangerousCallbacksDeny(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	notifier, methods := stage1FakeTelegram(t)
	notifier.inboundService = s
	before := reviewStat(t)
	for action, arity := range callbackArity {
		if action == "client_commands" || action == "client_traffic" || action == "client_get_usage" {
			continue
		}
		data := action
		for i := 1; i < arity[0]; i++ {
			data += " 1"
		}
		q := securityQuery(7, data)
		*methods = nil
		notifier.answerCallback(q, true)
		if len(*methods) != 1 || (*methods)[0] != "answerCallbackQuery" {
			t.Fatalf("%s escaped deny: %v", action, *methods)
		}
	}
	after := reviewStat(t)
	if before != after {
		t.Fatal("ordinary callbacks mutated isolated database traffic")
	}
	_, client, err := s.GetClientByEmail("review")
	if err != nil || client.TgID != 0 {
		t.Fatal("ordinary callbacks mutated binding")
	}
}
