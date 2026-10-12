package service

import (
	"fmt"
	"x-ui/database"
	"x-ui/database/model"
)

// Create commits the append-only reservation before any keyboard is sent.
// SQLite AUTOINCREMENT survives restart and does not recycle committed IDs.
// Never wrap or fall back to random IDs on exhaustion or a storage error.
func reserveTelegramBindingID() (int32, error) {
	db := database.GetDB()
	if db == nil {
		return 0, fmt.Errorf("binding database unavailable")
	}
	reservation := model.TelegramBindingSequence{}
	if err := db.Create(&reservation).Error; err != nil {
		return 0, err
	}
	if reservation.ID <= 0 || reservation.ID > 2147483648 {
		return 0, fmt.Errorf("Telegram binding request ID namespace exhausted")
	}
	return int32(-reservation.ID), nil
}
