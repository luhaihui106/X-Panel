package service

import (
	"encoding/json"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func TestMonthlyIdentitySaveFailureRollsBackMigration(t *testing.T) {
	s, c := reviewDB(t, true, false)
	p = xray.NewProcess(&xray.Config{})
	t.Cleanup(func() { p = nil })
	db := database.GetDB()
	if err := db.Exec(`CREATE TRIGGER reject_identity BEFORE UPDATE ON inbounds BEGIN SELECT RAISE(FAIL, 'identity'); END`).Error; err != nil {
		t.Fatal(err)
	}
	raw := `{"clients":[{"id":"new","email":"renamed","enable":false}]}`
	if _, err := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: raw}, c.ID); err == nil {
		t.Fatal("save must fail")
	}
	if stat := reviewStat(t); stat.Email != c.Email {
		t.Fatalf("failed identity save committed stat rename: %+v", stat)
	}
}

func TestMonthlyInboundRenamePreservesUsage(t *testing.T) {
	s, c := reviewDB(t, true, false)
	old, err := s.GetInbound(1)
	if err != nil {
		t.Fatal(err)
	}
	before := reviewStat(t)
	c.Email = "renamed"
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{c}})
	next := *old
	next.Settings = string(raw)
	if err := s.updateClientTraffics(database.GetDB(), old, &next); err != nil {
		t.Fatal(err)
	}
	after := reviewStat(t)
	if after.Id != before.Id || after.Up != before.Up || after.MonthlyResetAt != before.MonthlyResetAt || after.Email != c.Email {
		t.Fatalf("rename deleted usage/period: before=%+v after=%+v", before, after)
	}
}
