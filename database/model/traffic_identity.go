package model

// TrafficIdentity preserves counter ownership after a live identity is removed.
// Target is nonempty only for an email alias; Snapshot is the retired DB row.
// No expiry: successful removal does not fence counters from existing sessions.
type TrafficIdentity struct {
	Kind     string `gorm:"primaryKey"`
	Identity string `gorm:"primaryKey"`
	Target   string
	Snapshot string
	Up       int64
	Down     int64
	AllTime  int64
}
