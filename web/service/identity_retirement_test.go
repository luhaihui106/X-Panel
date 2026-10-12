package service

import (
	"encoding/json"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

// Independent reproduction: a supported live delete leaves old core counters.
func TestIndependentDeletedCounterMustNotPoisonCollector(t *testing.T) {
	s, old := reviewDB(t, true, false)
	old.Enable = true
	if err := database.GetDB().Exec(`UPDATE client_traffics SET enable=1,up=0,total=0,monthly_reset_day=0`).Error; err != nil {
		t.Fatal(err)
	}
	h := commitCore(t, false)
	_ = h
	second := model.Client{ID: "second", Email: "second", Enable: true}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{old, second}})
	db := database.GetDB()
	if err := db.Model(&model.Inbound{}).Where("id=1").Update("settings", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: 1, Email: second.Email, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	scheduledTraffic.pending = nil
	t.Cleanup(func() { scheduledTraffic.pending = nil })
	if _, err := s.DelInboundClient(1, old.ID); err != nil {
		t.Fatal(err)
	}
	err, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: old.Email, Up: 7}, {Email: second.Email, Up: 3}}, nil
	})
	if err != nil {
		t.Fatalf("supported delete poisons global collection: %v; pending=%v", err, scheduledTraffic.pending != nil)
	}
}

func TestIndependentCoreFailureDeltaExactlyOnce(t *testing.T) {
	s, c := reviewDB(t, false, true)
	h := commitCore(t, true)
	collector := &trafficCollector{}
	err, restart := collector.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 7}}, nil
	})
	if err != nil || !restart || collector.pending != nil || reviewStat(t).AllTime != 7 || h.calls.Load() != 1 {
		t.Fatalf("first commit err=%v restart=%v stat=%+v calls=%d", err, restart, reviewStat(t), h.calls.Load())
	}
	err, _ = collector.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return nil, nil, nil })
	if err != nil || reviewStat(t).AllTime != 7 || h.calls.Load() != 1 {
		t.Fatalf("committed usage retried: err=%v stat=%+v calls=%d", err, reviewStat(t), h.calls.Load())
	}
}
