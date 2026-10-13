package service

import (
	"os"
	"path/filepath"
	"testing"
	"x-ui/database"
)

func TestBindingGenerationOldBackupRestore(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	notifier, _ := stage1FakeTelegram(t)
	notifier.inboundService = s
	var rows []struct{ File string }
	if err := database.GetDB().Raw("PRAGMA database_list").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	path := rows[0].File
	backup := filepath.Join(t.TempDir(), "old-backup.db")
	if err := database.GetDB().Exec("VACUUM INTO ?", backup).Error; err != nil {
		t.Fatal(err)
	}
	notifier.clientTelegramUserInfo(42, "review")
	first := bindingRequests[42].id
	if first >= 0 {
		t.Fatal("expected negative request")
	}
	if err := database.CloseDB(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = database.InitDB(path); err != nil {
		t.Fatal(err)
	}
	notifier.clientTelegramUserInfo(42, "review")
	second := bindingRequests[42].id
	if second >= first {
		t.Fatalf("old backup reused issuance: old=%d new=%d", first, second)
	}
}
