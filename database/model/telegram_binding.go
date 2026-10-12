package model

// TelegramBindingSequence is an append-only issuance ledger. Never truncate it:
// negative Telegram IDs are the inverse of its durable SQLite sequence, keeping
// them disjoint from the legacy nonnegative random request IDs.
type TelegramBindingSequence struct {
	ID int64 `gorm:"primaryKey;autoIncrement"`
}
