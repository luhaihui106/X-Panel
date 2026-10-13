package service

import (
	"encoding/json"
	"github.com/mymmrac/telego"
	"strconv"
	"testing"
	"time"
	"x-ui/database"
)

func TestCallbackOwnershipOnRealDatabase(t *testing.T) {
	s, c := reviewDB(t, false, false)
	c.TgID = 7
	raw, _ := json.Marshal(map[string]any{"clients": []any{c}})
	if err := database.GetDB().Exec("UPDATE inbounds SET settings=? WHERE id=1", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	old := adminIds
	adminIds = []int64{42}
	defer func() { adminIds = old; consumedCallbacks = make(map[string]time.Time) }()
	notifier := &Tgbot{inboundService: s}
	before := reviewStat(t)
	if _, ok := notifier.authorizeCallback(securityQuery(8, "client_get_usage review")); ok {
		t.Fatal("other client allowed")
	}
	if _, ok := notifier.authorizeCallback(securityQuery(7, "client_get_usage review")); !ok {
		t.Fatal("own client denied")
	}
	after := reviewStat(t)
	if before.Up != after.Up || before.AllTime != after.AllTime {
		t.Fatal("query gate mutated traffic")
	}
}

func securityQuery(actor int64, data string) *telego.CallbackQuery {
	markup := secureKeyboard(actor, &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{{Text: "action", CallbackData: data}}}}).(*telego.InlineKeyboardMarkup)
	bindKeyboard(actor, 9, markup)
	return &telego.CallbackQuery{ID: strconv.FormatInt(actor, 10) + data, From: telego.User{ID: actor}, Data: markup.InlineKeyboard[0][0].CallbackData, Message: &telego.Message{MessageID: 9, Date: time.Now().Unix(), Chat: telego.Chat{ID: actor, Type: "private"}, ReplyMarkup: markup}}
}
func TestCallbackGateAdminReplayGroupAndWizard(t *testing.T) {
	oldAdmins := adminIds
	defer func() {
		adminIds = oldAdmins
		wizardSessions = map[int64]*telegramWizard{}
		consumedCallbacks = make(map[string]time.Time)
	}()
	adminIds = []int64{42, 43}
	consumedCallbacks = make(map[string]time.Time)
	notifier := &Tgbot{}
	q := securityQuery(42, "get_backup")
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("admin denied before dry dispatch")
	}
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("replay allowed")
	}
	q = securityQuery(42, "restart_panel_confirm")
	q.Message.(*telego.Message).Chat.Type = "group"
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("group allowed")
	}
	q = securityQuery(42, "unknown")
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("unknown allowed")
	}
	q = securityQuery(42, "get_backup")
	q.Message.(*telego.Message).Date = time.Now().Add(-21 * time.Minute).Unix()
	issued := issuedCallbacks[q.Data]
	issued.until = time.Now().Add(-time.Minute)
	issuedCallbacks[q.Data] = issued
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("expired allowed")
	}
	q = securityQuery(42, "get_backup")
	q.Message.(*telego.Message).ReplyMarkup = nil
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("removed button allowed")
	}
	q = securityQuery(42, "add_client")
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("admin wizard denied")
	}
	q = securityQuery(43, "add_client")
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("second administrator cannot start independent wizard")
	}
	q = securityQuery(42, "usage_refresh")
	oldKeyboard := q
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("rendered generation denied")
	}
	q = securityQuery(42, "usage_refresh")
	q.ID = "new-render"
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("regenerated refresh denied")
	}
	oldKeyboard.ID = "forged-old-generation"
	if _, ok := notifier.authorizeCallback(oldKeyboard); ok {
		t.Fatal("old keyboard generation accepted")
	}
	q = securityQuery(42, "usage_refresh")
	q.ID = "first-refresh"
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("first refresh denied")
	}
	q.ID = "fresh-click"
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("legitimate second refresh denied")
	}
	q = securityQuery(42, "add_client_submit_enable")
	wizardSessions[42].messages[9] = true
	wizardSessions[42].until = time.Now().Add(time.Minute)
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("current wizard submission denied")
	}
	q = securityQuery(42, "add_client_submit_disable")
	oldDraft := q
	q = securityQuery(42, "add_client")
	q.ID = "new-wizard"
	if _, ok := notifier.authorizeCallback(q); !ok {
		t.Fatal("new wizard denied")
	}
	q = oldDraft
	q.ID = "old-unused-button"
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("prior wizard message allowed in new session")
	}
	q = securityQuery(43, "add_client_submit_enable")
	wizardSessions[43].messages = map[int]bool{}
	if _, ok := notifier.authorizeCallback(q); ok {
		t.Fatal("second administrator can submit other's wizard")
	}
}
