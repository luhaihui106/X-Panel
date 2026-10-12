package service

import (
	"encoding/json"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func TestMonthlyLegacyEditPreservesFields(t *testing.T) {
	old := `{"clients":[{"id":"one","email":"a","monthlyResetEnabled":true,"monthlyResetDay":31}]}`
	updated, err := preserveMonthlyFields(old, `{"clients":[{"id":"one","email":"a","comment":"changed"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Clients []model.Client `json:"clients"`
	}
	json.Unmarshal([]byte(updated), &settings)
	if !settings.Clients[0].MonthlyResetEnabled || settings.Clients[0].MonthlyResetDay != 31 {
		t.Fatal(updated)
	}
	updated, err = preserveMonthlyFields(old, `{"clients":[{"id":"one","email":"a","monthlyResetEnabled":false,"monthlyResetDay":1}]}`)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(updated), &settings)
	if settings.Clients[0].MonthlyResetEnabled {
		t.Fatal("explicit disable ignored")
	}
}

func TestMonthlyScheduleSave(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&xray.ClientTraffic{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	s := InboundService{}
	c := model.Client{Email: "save", MonthlyResetEnabled: true, MonthlyResetDay: 31}
	if err = s.AddClientStat(db, 1, &c); err != nil {
		t.Fatal(err)
	}
	var stat xray.ClientTraffic
	db.First(&stat)
	if stat.MonthlyResetAt == 0 || stat.MonthlyResetDay != 31 {
		t.Fatalf("must initialize at save: %+v", stat)
	}
	marker := stat.MonthlyResetAt
	if err = s.UpdateClientStat(db, c.Email, &c); err != nil {
		t.Fatal(err)
	}
	db.First(&stat)
	if stat.MonthlyResetAt != marker {
		t.Fatal("ordinary edit loses cycle")
	}
	c.MonthlyResetEnabled = false
	if err = s.UpdateClientStat(db, c.Email, &c); err != nil {
		t.Fatal(err)
	}
	stat = xray.ClientTraffic{}
	db.First(&stat)
	if stat.MonthlyResetAt != 0 || stat.MonthlyResetDay != 0 {
		t.Fatal("disable did not clear enrollment")
	}
}

func TestMonthlyTrafficPipelineDrainsBeforeReset(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "monthly-sqlite-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err = database.InitDB(filepath.Join(dir, "panel.db")); err != nil {
		t.Fatal(err)
	}
	db := database.GetDB()
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	now := time.Now().UTC()
	day := now.Day()
	c := model.Client{Email: "drain", Enable: true, MonthlyResetEnabled: true, MonthlyResetDay: day}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{c}})
	if err = db.Create(&model.Inbound{Id: 1, Tag: "test", Settings: string(raw), Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&xray.ClientTraffic{InboundId: 1, Email: c.Email, Enable: true, Up: 100, Down: 200, MonthlyResetDay: day, MonthlyResetAt: now.AddDate(0, -1, 0).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Setting{Key: "timeLocation", Value: "UTC"})
	s := InboundService{}
	collector := &trafficCollector{}
	if err, _ := collector.collect(&s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 50, Down: 70}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var stat xray.ClientTraffic
	db.Where("email = ?", c.Email).First(&stat)
	if stat.Up != 0 || stat.Down != 0 || stat.AllTime != 120 {
		t.Fatalf("old core counters reintroduced: %+v", stat)
	}
	if err, _ := collector.collect(&s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 3, Down: 4}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	db.Where("email = ?", c.Email).First(&stat)
	if stat.Up != 3 || stat.Down != 4 {
		t.Fatalf("fresh traffic should remain: %+v", stat)
	}
}

func TestMonthlyValidation(t *testing.T) {
	s := InboundService{}
	for _, raw := range []string{`{"clients":[{"monthlyResetEnabled":true,"monthlyResetDay":0}]}`, `{"clients":[{"monthlyResetEnabled":true,"monthlyResetDay":32}]}`, `{"clients":[{"monthlyResetEnabled":true,"monthlyResetDay":1.5}]}`} {
		if _, err := s.GetClients(&model.Inbound{Settings: raw}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{"clients":[{"reset":7,"tgId":""}]}`, `{"clients":[{"monthlyResetEnabled":false,"monthlyResetDay":0}]}`, `{"clients":[{"monthlyResetEnabled":true,"monthlyResetDay":31}],"decryption":"none"}`} {
		if _, err := s.GetClients(&model.Inbound{Settings: raw}); err != nil {
			t.Fatalf("rejected %s: %v", raw, err)
		}
	}
}

func TestMonthlySQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.Inbound{}, &xray.ClientTraffic{}); err != nil {
		t.Fatal(err)
	}
	clients := []model.Client{
		{Email: "a", Enable: true, MonthlyResetEnabled: true, MonthlyResetDay: 31},
		{Email: "b", Enable: true, MonthlyResetEnabled: true, MonthlyResetDay: 15},
		{Email: "manual", Enable: false, MonthlyResetEnabled: true, MonthlyResetDay: 31},
		{Email: "expired", Enable: true, ExpiryTime: 1, MonthlyResetEnabled: true, MonthlyResetDay: 31},
		{Email: "legacy", Enable: true, Reset: 7},
	}
	raw, _ := json.Marshal(map[string]any{"clients": clients})
	in := model.Inbound{Id: 1, Settings: string(raw), Enable: true}
	if err = db.Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	for _, c := range clients {
		if err = db.Create(&xray.ClientTraffic{InboundId: 1, Email: c.Email, Up: 100, Down: 200, AllTime: 900, Enable: false, Total: 300, ExpiryTime: c.ExpiryTime, Reset: c.Reset}).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	s := InboundService{}
	if _, err = s.resetMonthlyClients(db, now); err != nil {
		t.Fatal(err)
	} // enrollment is not retroactive
	now = time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	restart, err := s.resetMonthlyClients(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if !restart {
		t.Fatal("quota recovery needs core restart")
	}
	check := func(email string, up int64, enable bool) {
		t.Helper()
		var stat xray.ClientTraffic
		if err := db.Where("email = ?", email).First(&stat).Error; err != nil {
			t.Fatal(err)
		}
		if stat.Up != up || stat.Enable != enable || stat.AllTime != 900 || stat.Total != 300 {
			t.Fatalf("%s: %+v", email, stat)
		}
	}
	check("a", 0, true)
	check("b", 100, false)
	check("manual", 0, false)
	check("expired", 0, false)
	check("legacy", 100, false)
	db.Model(&xray.ClientTraffic{}).Where("email = ?", "a").Update("up", 42)
	if _, err = s.resetMonthlyClients(db, now); err != nil {
		t.Fatal(err)
	}
	check("a", 42, true)
	// restart across several missed months only resets once, latest cycle.
	now = time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	if _, err = s.resetMonthlyClients(db, now); err != nil {
		t.Fatal(err)
	}
	check("a", 0, true)
	db.Model(&xray.ClientTraffic{}).Where("email = ?", "a").Update("up", 43)
	if _, err = s.resetMonthlyClients(db, now); err != nil {
		t.Fatal(err)
	}
	check("a", 43, true)
}

func TestMonthlyBoundary(t *testing.T) {
	for _, tc := range []struct {
		now  string
		day  int
		want string
	}{
		{"2025-02-28T00:00:00Z", 31, "2025-02-28"},
		{"2024-02-29T00:00:00Z", 30, "2024-02-29"},
		{"2026-04-30T00:00:00Z", 31, "2026-04-30"},
		{"2027-01-01T00:00:00Z", 31, "2026-12-31"},
		{"2026-03-28T00:00:00Z", 28, "2026-03-28"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := monthlyBoundary(now, tc.day).Format("2006-01-02"); got != tc.want {
			t.Errorf("%s day %d: %s != %s", tc.now, tc.day, got, tc.want)
		}
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse(time.RFC3339, "2026-03-30T16:00:00Z")
	if got := monthlyBoundary(now.In(loc), 31).Format("2006-01-02"); got != "2026-03-31" {
		t.Fatal(got)
	}
	if got := monthlyBoundary(now, 31).Format("2006-01-02"); got != "2026-02-28" {
		t.Fatal(got)
	}
}
