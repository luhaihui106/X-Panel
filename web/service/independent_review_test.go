package service

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func reviewDB(t *testing.T, manual bool, expired bool) (*InboundService, model.Client) {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "review.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.CloseDB() })
	now := time.Now().UTC()
	c := model.Client{ID: "old-id", Email: "review", Enable: !manual, MonthlyResetEnabled: true, MonthlyResetDay: now.Day(), TotalGB: 100}
	if expired {
		c.ExpiryTime = now.Add(-time.Hour).UnixMilli()
		c.Reset = 7
	}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{c}})
	db := database.GetDB()
	if err := db.Create(&model.Inbound{Id: 1, Enable: true, Tag: "review", Protocol: "vless", Settings: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: c.Email, Enable: false, Up: 100, Total: 100, ExpiryTime: c.ExpiryTime, Reset: c.Reset, MonthlyResetDay: c.MonthlyResetDay, MonthlyResetAt: now.AddDate(0, -1, 0).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Setting{Key: "timeLocation", Value: "UTC"})
	return &InboundService{}, c
}
func reviewStat(t *testing.T) xray.ClientTraffic {
	t.Helper()
	var v xray.ClientTraffic
	if err := database.GetDB().First(&v).Error; err != nil {
		t.Fatal(err)
	}
	return v
}
func TestReviewFailedDeltaMustNotReset(t *testing.T) {
	s, c := reviewDB(t, false, false)
	db := database.GetDB()
	if err := db.Exec(`CREATE TRIGGER reject_delta BEFORE UPDATE ON client_traffics WHEN NEW.up > OLD.up BEGIN SELECT RAISE(FAIL, 'injected delta failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	before := reviewStat(t)
	collector := &trafficCollector{}
	err, _ := collector.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 7}}, nil
	})
	v := reviewStat(t)
	if err == nil || v.Up != 100 || v.MonthlyResetAt != before.MonthlyResetAt {
		t.Fatalf("failed delta must abort reset: err=%v up=%d allTime=%d marker=%d", err, v.Up, v.AllTime, v.MonthlyResetAt)
	}
}
func TestReviewCommitFailureMustSurface(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	db := database.GetDB()
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(2)
	for _, q := range []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE parent_review(id INTEGER PRIMARY KEY)`, `CREATE TABLE child_review(id INTEGER REFERENCES parent_review(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER reject_commit AFTER UPDATE OF monthly_reset_at ON client_traffics BEGIN INSERT INTO child_review VALUES(999); END`} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	err, restart := s.storeTraffic(nil, nil, true)
	v := reviewStat(t)
	if err == nil || restart || v.Up != 100 {
		t.Fatalf("commit error hidden: err=%v restart=%v persistedUp=%d", err, restart, v.Up)
	}
}
func TestReviewNilDrainMustNotReset(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	err, _ := s.AddTraffic(nil, nil)
	v := reviewStat(t)
	if err != nil {
		t.Fatal(err)
	}
	if v.Up != 100 {
		t.Fatalf("undrained empty caller reset up=%d", v.Up)
	}
}
func TestReviewExpiredManualMustRemainDisabled(t *testing.T) {
	s, _ := reviewDB(t, true, true)
	err, _ := s.AddTraffic(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := reviewStat(t)
	if v.Enable {
		t.Fatalf("monthly + autoRenew re-enabled manual-disabled stat: %+v", v)
	}
}
func TestReviewLatePredrainedBatchMustNotReintroduceOldCycle(t *testing.T) {
	s, c := reviewDB(t, false, false)
	// Arbitrary pre-drained callers cannot certify a period boundary. Real
	// scheduler ordering is covered by TestTrafficCollectionOverlapAndRetry.
	oldBatch := []*xray.ClientTraffic{{Email: c.Email, Up: 17}}
	// Neither arbitrary caller may reset the monthly period.
	if err, _ := s.AddTraffic(nil, []*xray.ClientTraffic{{Email: c.Email, Up: 3}}); err != nil {
		t.Fatal(err)
	}
	if err, _ := s.AddTraffic(nil, oldBatch); err != nil {
		t.Fatal(err)
	}
	v := reviewStat(t)
	if v.Up != 120 {
		t.Fatalf("pre-drained caller unexpectedly reset cycle: up=%d", v.Up)
	}
}
func TestReviewIdentityChangePreservesSchedule(t *testing.T) {
	s, c := reviewDB(t, true, false)
	before := reviewStat(t)
	p = xray.NewProcess(&xray.Config{})
	t.Cleanup(func() { p = nil })
	raw := `{"clients":[{"id":"new-id","email":"new-email","enable":false,"totalGB":100}]}`
	// Use the actual old-client-ID endpoint, not an identity-less helper.
	_, err := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: raw}, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := s.GetInbound(1)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := s.GetClients(inbound)
	if err != nil {
		t.Fatal(err)
	}
	after := reviewStat(t)
	if after.Enable || !clients[0].MonthlyResetEnabled || clients[0].MonthlyResetDay != c.MonthlyResetDay || after.MonthlyResetAt != before.MonthlyResetAt || after.Up != before.Up || after.Email != "new-email" {
		t.Fatalf("identity edit lost schedule/usage/disable: client=%+v stat=%+v", clients[0], after)
	}
}
func TestReviewRestartIdempotence(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	db := database.GetDB()
	collector := &trafficCollector{}
	if err, _ := collector.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return nil, nil, nil }); err != nil {
		t.Fatal(err)
	}
	v := reviewStat(t)
	db.Model(&v).Update("up", 42)
	var name string
	row := db.Raw("PRAGMA database_list").Row()
	var idx int
	var kind string
	if err := row.Scan(&idx, &kind, &name); err != nil {
		t.Fatal(err)
	}
	database.CloseDB()
	if err := database.InitDB(name); err != nil {
		t.Fatal(err)
	}
	if err, _ := collector.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return nil, nil, nil }); err != nil {
		t.Fatal(err)
	}
	v = reviewStat(t)
	if v.Up != 42 {
		t.Fatalf("restart repeats reset: %+v", v)
	}
}
