package service

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"os"
	"path/filepath"
	"x-ui/config"
	"x-ui/database"
)

// This trusted ledger is outside the replaceable business database. Preserve it
// when moving installations; business backup imports must never replace it.
// FULL synchronous commits and immediate transactions serialize reservations
// across processes before a request keyboard can be published.
func reserveTelegramBindingID() (int32, error) {
	if database.GetDB() == nil {
		return 0, fmt.Errorf("binding database unavailable")
	}
	var legacy int64
	if err := database.GetDB().Raw("SELECT COALESCE(MAX(id),0) FROM telegram_binding_sequences").Scan(&legacy).Error; err != nil {
		return 0, err
	}
	if err := os.MkdirAll(config.GetDBFolderPath(), 0700); err != nil {
		return 0, err
	}
	ledgerPath := filepath.Join(config.GetDBFolderPath(), "telegram-binding-issuance.db")
	dsn := (&url.URL{Scheme: "file", Path: ledgerPath}).String() + "?_txlock=immediate&_busy_timeout=5000&_synchronous=FULL&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS issuance (id INTEGER PRIMARY KEY CHECK(id=1), high INTEGER NOT NULL CHECK(high>=0))"); err != nil {
		return 0, err
	}
	if err = os.Chmod(ledgerPath, 0600); err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT OR IGNORE INTO issuance(id,high) VALUES(1,0)"); err != nil {
		return 0, err
	}
	var high int64
	if err = tx.QueryRow("SELECT high FROM issuance WHERE id=1").Scan(&high); err != nil {
		return 0, err
	}
	if legacy > high {
		high = legacy
	}
	if high >= 2147483648 {
		return 0, fmt.Errorf("Telegram binding request ID namespace exhausted")
	}
	high++
	if _, err = tx.Exec("UPDATE issuance SET high=? WHERE id=1", high); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int32(-high), nil
}
