package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/xtls/xray-core/app/proxyman/command"
	"strings"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func (h *commitHandler) AddInbound(context.Context, *command.AddInboundRequest) (*command.AddInboundResponse, error) {
	h.calls.Add(1)
	if h.fail {
		return nil, fmt.Errorf("injected add inbound core failure")
	}
	return &command.AddInboundResponse{}, nil
}

func TestAddInboundImportTransactionFailureDoesNotMutateCore(t *testing.T) {
	for _, failure := range []string{"write", "commit"} {
		t.Run(failure, func(t *testing.T) {
			s, _, _ := identityPair(t)
			h := commitCore(t, false)
			db := database.GetDB()
			sqlDB, _ := db.DB()
			sqlDB.SetMaxOpenConns(1)
			queries := []string{`CREATE TRIGGER import_failure BEFORE INSERT ON client_traffics BEGIN SELECT RAISE(FAIL,'import write failure'); END`}
			if failure == "commit" {
				queries = []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE import_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE import_child(id INTEGER REFERENCES import_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER import_failure AFTER INSERT ON client_traffics BEGIN INSERT INTO import_child VALUES(999); END`}
			}
			for _, q := range queries {
				if err := db.Exec(q).Error; err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(map[string]any{"clients": []model.Client{{ID: "fresh", Email: "fresh", Enable: true}}})
			in := &model.Inbound{Enable: true, Port: 34567, Tag: "imported", Protocol: "vless", Settings: string(raw), ClientStats: []xray.ClientTraffic{{Email: "fresh", Up: 13}}}
			if _, restart, err := s.AddInbound(in); err == nil || restart {
				t.Fatalf("failed transaction result: restart=%v err=%v", restart, err)
			}
			if h.calls.Load() != 0 {
				t.Fatal("failed transaction mutated core")
			}
			var count int64
			if err := db.Model(&model.Inbound{}).Where("tag=?", "imported").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("inbound survived rollback: %d %v", count, err)
			}
			if err := db.Model(&xray.ClientTraffic{}).Where("email=?", "fresh").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("stats survived rollback: %d %v", count, err)
			}
		})
	}
}

func TestAddInboundImportedStatsValidateSettings(t *testing.T) {
	for _, kind := range []string{"reserved", "unrelated", "missing", "duplicate", "case-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			s, old, _ := identityPair(t)
			h := commitCore(t, false)
			if _, err := s.DelInboundClient(1, old.ID); err != nil {
				t.Fatal(err)
			}
			before := h.calls.Load()
			clients := []model.Client{{ID: "fresh", Email: "fresh", Enable: true}}
			stats := []xray.ClientTraffic{{Email: "fresh", Enable: true}}
			switch kind {
			case "reserved":
				clients[0].Email = old.Email
				stats[0].Email = "unrelated"
			case "unrelated":
				stats[0].Email = "unrelated"
			case "missing":
				clients = append(clients, model.Client{ID: "extra", Email: "extra", Enable: true})
			case "duplicate":
				stats = append(stats, stats[0])
			case "case-mismatch":
				stats[0].Email = "FRESH"
			}
			raw, _ := json.Marshal(map[string]any{"clients": clients})
			in := &model.Inbound{Enable: true, Port: 34567, Tag: "imported", Protocol: "vless", Settings: string(raw), ClientStats: stats}
			_, restart, err := s.AddInbound(in)
			if err == nil {
				t.Fatal("unsafe imported stats accepted")
			}
			if kind == "reserved" && !strings.Contains(err.Error(), "reserved for unsettled core counters") {
				t.Fatal(err)
			}
			if restart || h.calls.Load() != before {
				t.Fatal("rejected import mutated core")
			}
			var count int64
			if err := database.GetDB().Model(&model.Inbound{}).Where("tag=?", "imported").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("inbound persisted: %d %v", count, err)
			}
			if err := database.GetDB().Model(&xray.ClientTraffic{}).Where("email IN ?", []string{"fresh", "FRESH", "extra", "unrelated", old.Email}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("stats persisted: %d %v", count, err)
			}
		})
	}
}
