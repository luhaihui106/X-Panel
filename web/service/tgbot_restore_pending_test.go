package service

import (
	"os"
	"path/filepath"
	"testing"
	"x-ui/config"
	"x-ui/database"
	"x-ui/database/model"
)

func TestImportDBRevokesPendingBeforeClientIDReuse(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	n.xrayService = &XrayService{}
	path := config.GetDBPath()
	// The old backup uses the same traffic ID but belongs to a different client.
	if err := database.GetDB().Exec("UPDATE client_traffics SET email='restored-client'").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&model.Inbound{}).Where("id=1").Update("settings", `{"clients":[{"id":"restored-id","email":"restored-client","enable":true}]}`).Error; err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "old.db")
	if err := database.GetDB().Exec("VACUUM INTO ?", backup).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Exec("UPDATE client_traffics SET email='review'").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&model.Inbound{}).Where("id=1").Update("settings", `{"clients":[{"id":"current-id","email":"review","enable":true}]}`).Error; err != nil {
		t.Fatal(err)
	}
	n.clientTelegramUserInfo(42, "review")
	id := bindingRequests[42].id
	current := filepath.Join(t.TempDir(), "current.db")
	if err := database.GetDB().Exec("VACUUM INTO ?", current).Error; err != nil {
		t.Fatal(err)
	}
	// Put this isolated DB at ImportDB's configured target, with no core attached.
	if err := database.CloseDB(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = database.InitDB(path); err != nil {
		t.Fatal(err)
	}
	server := &ServerService{}
	server.importCoreEffects = &databaseImportCoreEffects{stop: func() {}, restart: func() error { return nil }}
	f, err := os.Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = server.ImportDB(f); err != nil {
		t.Fatal(err)
	}
	n.handleTelegramMessage(sharedMessage(42, id, 900))
	_, c, err := s.GetClientByEmail("restored-client")
	if err != nil || c.TgID != 0 {
		t.Fatalf("late sharing bound restored different client: %+v %v", c, err)
	}
	if len(bindingRequests) != 0 {
		t.Fatal("import left old pending")
	}

}
