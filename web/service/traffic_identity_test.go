package service

import (
	"encoding/json"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func identityPair(t *testing.T) (*InboundService, model.Client, model.Client) {
	t.Helper()
	s, old := reviewDB(t, true, false)
	old.Enable = true
	old.MonthlyResetEnabled = false
	old.TotalGB = 0
	second := model.Client{ID: "second", Email: "second", Enable: true}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{old, second}})
	db := database.GetDB()
	if e := db.Model(&model.Inbound{}).Where("id=1").Update("settings", string(raw)).Error; e != nil {
		t.Fatal(e)
	}
	if e := db.Exec(`UPDATE client_traffics SET enable=1,up=0,total=0,monthly_reset_day=0`).Error; e != nil {
		t.Fatal(e)
	}
	if e := db.Create(&xray.ClientTraffic{InboundId: 1, Email: second.Email, Enable: true}).Error; e != nil {
		t.Fatal(e)
	}
	scheduledTraffic.pending = nil
	t.Cleanup(func() { scheduledTraffic.pending = nil })
	return s, old, second
}
func identityDelta(old, next string) trafficDrain {
	return func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: old, Up: 7}, {Email: next, Up: 3}}, nil
	}
}
func identityOwner(t *testing.T, email string) model.TrafficIdentity {
	t.Helper()
	var v model.TrafficIdentity
	if e := database.GetDB().Where("kind='client' AND identity=?", email).First(&v).Error; e != nil {
		t.Fatal(e)
	}
	return v
}
func TestIdentityPendingDeleteAndReservedEmail(t *testing.T) {
	s, old, next := identityPair(t)
	commitCore(t, false)
	db := database.GetDB()
	db.Exec(`CREATE TRIGGER identity_pending BEFORE UPDATE ON client_traffics WHEN NEW.up > OLD.up BEGIN SELECT RAISE(FAIL,'delta'); END`)
	if e, _ := s.CollectTraffic(identityDelta(old.Email, next.Email)); e == nil {
		t.Fatal("injection")
	}
	if _, e := s.DelInboundClient(1, old.ID); e != nil {
		t.Fatal(e)
	}
	db.Exec(`DROP TRIGGER identity_pending`)
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		t.Fatal("retry redrained")
		return nil, nil, nil
	}); e != nil {
		t.Fatal(e)
	}
	if v := identityOwner(t, old.Email); v.AllTime != 7 {
		t.Fatal(v)
	}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{{ID: "fresh", Email: "REVIEW", Enable: true}}})
	if _, e := s.AddInboundClient(&model.Inbound{Id: 1, Settings: string(raw)}); e == nil {
		t.Fatal("retired email reused")
	}
	// Unknown traffic is still a real error; known retirement is not.
	if e, _ := s.CollectTraffic(identityDelta("not-enrolled", next.Email)); e == nil {
		t.Fatal("unknown silently discarded")
	}
}

func TestIdentityRenameChainAndPersistence(t *testing.T) {
	s, old, next := identityPair(t)
	commitCore(t, false)
	db := database.GetDB()
	for _, email := range []string{"renamed", "twice"} {
		old.Email = email
		raw, _ := json.Marshal(map[string]any{"clients": []model.Client{old}})
		if _, e := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: string(raw)}, old.ID); e != nil {
			t.Fatal(e)
		}
	}
	if e, _ := s.CollectTraffic(identityDelta("review", "renamed")); e != nil {
		t.Fatal(e)
	}
	var stat xray.ClientTraffic
	db.Where("email='twice'").First(&stat)
	if stat.AllTime != 10 {
		t.Fatal(stat)
	}
	if _, e := s.DelInboundClient(1, old.ID); e != nil {
		t.Fatal(e)
	}
	// Reopen the actual migrated SQLite database, losing all in-memory ownership.
	var dbFiles []struct{ File string }
	db.Raw("PRAGMA database_list").Scan(&dbFiles)
	if len(dbFiles) == 0 {
		t.Fatal("no DB file")
	}
	if e := database.CloseDB(); e != nil {
		t.Fatal(e)
	}
	if e := database.InitDB(dbFiles[0].File); e != nil {
		t.Fatal(e)
	}
	if e, _ := s.CollectTraffic(identityDelta("review", next.Email)); e != nil {
		t.Fatal(e)
	}
	if v := identityOwner(t, "twice"); v.AllTime != 17 {
		t.Fatal(v)
	}
}

func TestIdentityRollbackDoesNotPublishOnlineClients(t *testing.T) {
	s, old, _ := identityPair(t)
	commitCore(t, false)
	db := database.GetDB()
	p.SetOnlineClients([]string{"before"})
	db.Exec(`CREATE TRIGGER online_reject BEFORE UPDATE ON client_traffics BEGIN SELECT RAISE(FAIL,'online'); END`)
	if e, _ := s.CollectTraffic(identityDelta(old.Email, "second")); e == nil {
		t.Fatal("injection")
	}
	if p.GetOnlineClients()[0] != "before" {
		t.Fatal("uncommitted online state published")
	}
}

func TestIdentityInboundDeleteIdempotent(t *testing.T) {
	s, _, _ := identityPair(t)
	h := commitCore(t, false)
	if _, e := s.DelInbound(1); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DelInbound(1); e != nil {
		t.Fatalf("repeat inbound delete: %v", e)
	}
	if h.calls.Load() != 1 {
		t.Fatal("repeat core mutation")
	}
}

func TestIdentityTagChangeRoutesLateCounters(t *testing.T) {
	s, _, _ := identityPair(t)
	commitCore(t, false)
	in, _ := s.GetInbound(1)
	in.Port = 23456
	if _, _, e := s.UpdateInbound(in); e != nil {
		t.Fatal(e)
	}
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return []*xray.Traffic{{Tag: "review", IsInbound: true, Up: 9}}, nil, nil
	}); e != nil {
		t.Fatal(e)
	}
	in, _ = s.GetInbound(1)
	if in.AllTime != 9 {
		t.Fatalf("old tag usage lost: %+v", in)
	}
}

func TestIdentityInboundLateAndReuse(t *testing.T) {
	s, _, _ := identityPair(t)
	commitCore(t, false)
	if _, e := s.DelInbound(1); e != nil {
		t.Fatal(e)
	}
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return []*xray.Traffic{{Tag: "review", IsInbound: true, Up: 11}}, nil, nil
	}); e != nil {
		t.Fatal(e)
	}
	var owner model.TrafficIdentity
	if e := database.GetDB().Where("kind='inbound' AND identity='review'").First(&owner).Error; e != nil {
		t.Fatal(e)
	}
	if owner.AllTime != 11 {
		t.Fatalf("late inbound lost: %+v", owner)
	}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{{ID: "fresh", Email: "fresh"}}})
	if _, _, e := s.AddInbound(&model.Inbound{Port: 12345, Tag: "review", Protocol: "vless", Settings: string(raw)}); e == nil {
		t.Fatal("retired tag reused")
	}
}

func TestIdentityRemovalRetryAndRollback(t *testing.T) {
	for _, failure := range []string{"write", "commit", "core"} {
		t.Run(failure, func(t *testing.T) {
			s, old, next := identityPair(t)
			h := commitCore(t, failure == "core")
			db := database.GetDB()
			if failure == "write" {
				db.Exec(`CREATE TRIGGER retirement_reject BEFORE INSERT ON traffic_identities BEGIN SELECT RAISE(FAIL,'retire'); END`)
			}
			if failure == "commit" {
				sq, _ := db.DB()
				sq.SetMaxOpenConns(2)
				for _, q := range []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE retirement_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE retirement_child(id INTEGER REFERENCES retirement_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER retirement_reject AFTER DELETE ON client_traffics BEGIN INSERT INTO retirement_child VALUES(999); END`} {
					if e := db.Exec(q).Error; e != nil {
						t.Fatal(e)
					}
				}
			}
			restart, e := s.DelInboundClient(1, old.ID)
			if failure != "core" {
				if e == nil || restart || h.calls.Load() != 0 {
					t.Fatalf("uncommitted removal: %v %v %d", e, restart, h.calls.Load())
				}
				var count int64
				db.Model(&model.TrafficIdentity{}).Count(&count)
				if count != 0 {
					t.Fatal("rollback left owner")
				}
				db.Exec(`DROP TRIGGER retirement_reject`)
				if _, e = s.DelInboundClient(1, old.ID); e != nil {
					t.Fatal(e)
				}
			} else if e != nil || !restart {
				t.Fatalf("core failure not reconciliation: %v %v", e, restart)
			}
			if _, e = s.DelInboundClient(1, old.ID); e != nil {
				t.Fatalf("idempotent delete: %v", e)
			}
			db.Exec(`CREATE TRIGGER retirement_delta BEFORE UPDATE ON traffic_identities WHEN NEW.up>OLD.up BEGIN SELECT RAISE(FAIL,'delta'); END`)
			if e, _ = s.CollectTraffic(identityDelta(old.Email, next.Email)); e == nil {
				t.Fatal("delta injection")
			}
			if v := identityOwner(t, old.Email); v.AllTime != 0 {
				t.Fatal("rollback counted retired delta")
			}
			db.Exec(`DROP TRIGGER retirement_delta`)
			if e, _ = s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
				t.Fatal("retry redrained")
				return nil, nil, nil
			}); e != nil {
				t.Fatal(e)
			}
			if v := identityOwner(t, old.Email); v.AllTime != 7 {
				t.Fatalf("retired delta not exact once %+v", v)
			}
			if e, _ = s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
				return nil, []*xray.ClientTraffic{{Email: next.Email, Up: 4}}, nil
			}); e != nil {
				t.Fatal(e)
			}
			var stat xray.ClientTraffic
			db.Where("email=?", next.Email).First(&stat)
			if stat.AllTime != 7 {
				t.Fatalf("healthy stopped %+v", stat)
			}
		})
	}
}

func TestIdentityRenamePendingAndLate(t *testing.T) {
	s, old, next := identityPair(t)
	commitCore(t, false)
	db := database.GetDB()
	if e := db.Exec(`CREATE TRIGGER identity_delta BEFORE UPDATE ON client_traffics WHEN NEW.up > OLD.up BEGIN SELECT RAISE(FAIL,'delta'); END`).Error; e != nil {
		t.Fatal(e)
	}
	if e, _ := s.CollectTraffic(identityDelta(old.Email, next.Email)); e == nil {
		t.Fatal("failure injection")
	}
	old.Email = "renamed"
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{old}})
	if _, e := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: string(raw)}, old.ID); e != nil {
		t.Fatalf("rename must preserve pending ownership: %v", e)
	}
	db.Exec(`DROP TRIGGER identity_delta`)
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		t.Fatal("retry redrained")
		return nil, nil, nil
	}); e != nil {
		t.Fatal(e)
	}
	if e, _ := s.CollectTraffic(identityDelta("review", "renamed")); e != nil {
		t.Fatal(e)
	}
	var stat xray.ClientTraffic
	db.Where("email='renamed'").First(&stat)
	if stat.AllTime != 17 {
		t.Fatalf("alias/live deltas lost or repeated: %+v", stat)
	}
	if _, e := s.DelInboundClient(1, old.ID); e != nil {
		t.Fatal(e)
	}
	if e, _ := s.CollectTraffic(identityDelta("review", "renamed")); e != nil {
		t.Fatal(e)
	}
	if v := identityOwner(t, "renamed"); v.AllTime != 27 {
		t.Fatalf("renamed retirement: %+v", v)
	}
}

func TestIdentityRemovedOwnership(t *testing.T) {
	for _, kind := range []string{"inbound", "depleted", "bulk"} {
		t.Run(kind, func(t *testing.T) {
			s, old, next := identityPair(t)
			commitCore(t, false)
			db := database.GetDB()
			switch kind {
			case "inbound":
				// Healthy client lives in another inbound for a whole-inbound removal.
				db.Model(&xray.ClientTraffic{}).Where("email=?", next.Email).Update("inbound_id", 2)
				raw, _ := json.Marshal(map[string]any{"clients": []model.Client{next}})
				db.Create(&model.Inbound{Id: 2, Protocol: "vless", Tag: "healthy", Settings: string(raw)})
				if _, e := s.DelInbound(1); e != nil {
					t.Fatal(e)
				}
			case "depleted":
				db.Model(&xray.ClientTraffic{}).Where("email=?", old.Email).Update("enable", false)
				if e := s.DelDepletedClients(1); e != nil {
					t.Fatal(e)
				}
			case "bulk":
				in, _ := s.GetInbound(1)
				raw, _ := json.Marshal(map[string]any{"clients": []model.Client{next}})
				in.Settings = string(raw)
				if _, _, e := s.UpdateInbound(in); e != nil {
					t.Fatal(e)
				}
			}
			if e, _ := s.CollectTraffic(identityDelta(old.Email, next.Email)); e != nil {
				t.Fatal(e)
			}
			if v := identityOwner(t, old.Email); v.AllTime != 7 {
				t.Fatalf("retired delta lost: %+v", v)
			}
			var healthy xray.ClientTraffic
			db.Where("email=?", next.Email).First(&healthy)
			if healthy.AllTime != 3 {
				t.Fatalf("healthy delta lost: %+v", healthy)
			}
		})
	}
}
