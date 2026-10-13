package service

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"x-ui/config"
)

func TestBindingGenerationCommitFailureRetryReopen(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	notifier, methods := stage1FakeTelegram(t)
	notifier.inboundService = s
	notifier.clientTelegramUserInfo(42, "review")
	first := bindingRequests[42].id
	path := filepath.Join(config.GetDBFolderPath(), "telegram-binding-issuance.db")
	ledger, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE parent(id INTEGER PRIMARY KEY)", "CREATE TABLE child(id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)", "CREATE TRIGGER reject_commit AFTER UPDATE ON issuance BEGIN INSERT INTO child VALUES(999); END"} {
		if _, err = ledger.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// Prove the injected fault occurs at COMMIT, not UPDATE.
	tx, err := ledger.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE issuance SET high=high+1"); err != nil {
		t.Fatalf("update failed before commit: %v", err)
	}
	if err = tx.Commit(); err == nil {
		t.Fatal("expected deferred constraint COMMIT failure")
	}
	*methods = nil
	notifier.clientTelegramUserInfo(42, "review")
	if _, ok := bindingRequests[42]; ok {
		t.Fatal("pending survived failed COMMIT")
	}
	calls := strings.Join(*methods, ",")
	if strings.Contains(calls, "request_users") || !strings.Contains(calls, "remove_keyboard") {
		t.Fatalf("COMMIT failure published keyboard: %v", *methods)
	}
	if _, err = ledger.Exec("DROP TRIGGER reject_commit"); err != nil {
		t.Fatal(err)
	}
	if err = ledger.Close(); err != nil {
		t.Fatal(err)
	}
	notifier.clientTelegramUserInfo(42, "review")
	second := bindingRequests[42].id
	if second != first-1 {
		t.Fatalf("retry/reopen high water: %d %d", first, second)
	}
}

func TestBindingGenerationConcurrentReservations(t *testing.T) {
	reviewDB(t, false, false)
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	var wg sync.WaitGroup
	ids := make(chan int32, 24)
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := reserveTelegramBindingID()
			if err != nil {
				errs <- err
			} else {
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[int32]bool{}
	for id := range ids {
		if id >= 0 || seen[id] {
			t.Fatalf("reused/invalid ID %d", id)
		}
		seen[id] = true
	}
	if len(seen) != 24 {
		t.Fatal("missing reservations")
	}
}
