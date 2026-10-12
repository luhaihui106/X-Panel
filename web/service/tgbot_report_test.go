package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
)

type reportOfflineTransport struct{ t *testing.T }

func (tr reportOfflineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.t.Error("scheduled report attempted an external fetch")
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Request: r, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"disabled-in-test"}`))}, nil
}

// Exercise the actual report and stored notification settings entirely offline.
func TestScheduledReportUsesOnlyConfiguredNotificationBot(t *testing.T) {
	assertConfiguredBotConstruction(t)
	dir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "notification-report-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := database.InitDB(filepath.Join(dir, "panel.db")); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := database.GetDB().DB()
	defer sqlDB.Close()
	settings := &SettingService{}
	if err := database.GetDB().Create(&model.Setting{Key: "timeLocation", Value: "Asia/Shanghai"}).Error; err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("1", 9) + ":" + strings.Repeat("a", 35)
	if err := settings.SetTgBotToken(token); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetTgbotRuntime("local-test"); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.GetTgBotToken()
	if err != nil || saved != token {
		t.Fatal("notification token persistence failed")
	}
	var mu sync.Mutex
	var messages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ChatID != 42 {
			t.Error("wrong configured recipient or payload")
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Error("unexpected notification method")
		}
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			t.Error("unconfigured bot used")
		}
		if !strings.Contains(payload.Text, "管理员运营摘要") || !strings.Contains(payload.Text, "服务器状态") || !strings.Contains(payload.Text, "Asia/Shanghai") || !strings.Contains(payload.Text, "+0800") {
			t.Error("operations summary missing real status/timezone information")
		}
		for _, retired := range []string{"抽奖", "美图", "新闻", "每日一语", "购买", "任务:"} {
			if strings.Contains(payload.Text, retired) {
				t.Error("retired report content remains")
			}
		}
		if len(payload.Text) > 2000 {
			t.Error("report exceeds single-message budget")
		}
		mu.Lock()
		messages++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`)
	}))
	defer server.Close()
	oldBot, oldAdmins, oldRunning, oldTransport := bot, adminIds, isRunning, http.DefaultTransport
	defer func() { bot, adminIds, isRunning, http.DefaultTransport = oldBot, oldAdmins, oldRunning, oldTransport }()
	notifier := &Tgbot{settingService: settings}
	bot, err = notifier.NewBot(saved, "", server.URL)
	if err != nil {
		t.Fatal("local notification bot creation failed")
	}
	adminIds, isRunning, http.DefaultTransport = []int64{42}, true, reportOfflineTransport{t: t}
	notifier.SendReport()
	mu.Lock()
	defer mu.Unlock()
	if messages != 1 {
		t.Fatal("expected one administrator operations summary")
	}
}
