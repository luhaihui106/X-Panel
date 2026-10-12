package service

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/xray"
)

func TestTrafficCollectionActivationSaveFailureRetainsBatch(t *testing.T) {
	s, c := reviewDB(t, false, false)
	db := database.GetDB()
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", c.Email).Update("expiry_time", -86400000).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_activation BEFORE UPDATE ON inbounds BEGIN SELECT RAISE(FAIL, 'activation'); END`).Error; err != nil {
		t.Fatal(err)
	}
	collector := &trafficCollector{}
	drain := func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 7}}, nil
	}
	if err, _ := collector.collect(s, drain); err == nil {
		t.Fatal("activation settings Save error swallowed")
	}
	if collector.pending == nil || reviewStat(t).ExpiryTime != -86400000 {
		t.Fatal("failed activation was not rolled back/retained")
	}
}

func TestTrafficCollectionNotifiesOnlyCommittedBatch(t *testing.T) {
	s, c := reviewDB(t, false, false)
	collector := &trafficCollector{}
	db := database.GetDB()
	if err := db.Exec(`CREATE TRIGGER reject_notify BEFORE UPDATE ON client_traffics WHEN NEW.up > OLD.up BEGIN SELECT RAISE(FAIL, 'notify'); END`).Error; err != nil {
		t.Fatal(err)
	}
	notified := 0
	notify := func(inbound []*xray.Traffic, clients []*xray.ClientTraffic) {
		notified++
		if len(clients) != 1 || clients[0].Up != 7 {
			t.Fatalf("wrong notified batch: %+v", clients)
		}
	}
	drain := func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 7}}, nil
	}
	if err, _ := collector.collect(s, drain, notify); err == nil {
		t.Fatal("missing injected failure")
	}
	if notified != 0 {
		t.Fatal("notified uncommitted batch")
	}
	db.Exec(`DROP TRIGGER reject_notify`)
	if err, _ := collector.collect(s, drain, notify); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Fatalf("committed retry notified %d times", notified)
	}
}

func TestTrafficCollectionOverlapAndRetry(t *testing.T) {
	s, c := reviewDB(t, false, false)
	collector := &trafficCollector{}
	started, release := make(chan struct{}), make(chan struct{})
	var drains atomic.Int32
	drain := func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		n := drains.Add(1)
		if n == 1 {
			close(started)
			<-release
		}
		return nil, []*xray.ClientTraffic{{Email: c.Email, Up: int64(n) * 17}}, nil
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { err, _ := collector.collect(s, drain); first <- err }()
	<-started
	go func() { err, _ := collector.collect(s, drain); second <- err }()
	time.Sleep(30 * time.Millisecond)
	if drains.Load() != 1 {
		t.Errorf("overlapping job drained before earlier batch committed: %d", drains.Load())
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	v := reviewStat(t)
	if v.Up != 34 || v.AllTime != 51 {
		t.Fatalf("wrong ordered periods: %+v", v)
	}
	db := database.GetDB()
	if err := db.Exec(`CREATE TRIGGER reject_retry BEFORE UPDATE ON client_traffics WHEN NEW.up > OLD.up BEGIN SELECT RAISE(FAIL, 'retry'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err, _ := collector.collect(s, drain); err == nil {
		t.Fatal("expected failed batch")
	}
	if drains.Load() != 3 {
		t.Fatal("missing drain")
	}
	if err, _ := collector.collect(s, drain); err == nil {
		t.Fatal("expected retry failure")
	}
	if drains.Load() != 3 {
		t.Fatal("must retry pending before another destructive read")
	}
	db.Exec(`DROP TRIGGER reject_retry`)
	if err, _ := collector.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, nil, errors.New("no drain expected")
	}); err != nil {
		t.Fatal(err)
	}
	v = reviewStat(t)
	if v.Up != 85 || v.AllTime != 102 {
		t.Fatalf("retry lost or duplicated batch: %+v", v)
	}
}
