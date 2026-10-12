package service

import (
	"encoding/json"
	"reflect"
	"testing"
	"x-ui/database"
	"x-ui/xray"
)

func TestRetiredClientSnapshotPersistsAllFields(t *testing.T) {
	s, old, next := identityPair(t)
	commitCore(t, false)
	db := database.GetDB()
	var original xray.ClientTraffic
	if err := db.Where("email=?", old.Email).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	original.Enable = false
	original.Up = 12
	original.Down = 5
	original.AllTime = 30
	original.ExpiryTime = 123456
	original.Total = 999
	original.Reset = 2
	original.MonthlyResetDay = 12
	original.MonthlyResetAt = 123
	original.LastOnline = 456
	if err := db.Save(&original).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.DelInboundClient(1, old.ID); err != nil {
		t.Fatal(err)
	}
	var files []struct{ File string }
	if err := db.Raw("PRAGMA database_list").Scan(&files).Error; err != nil || len(files) == 0 {
		t.Fatal(err)
	}
	if err := database.CloseDB(); err != nil {
		t.Fatal(err)
	}
	if err := database.InitDB(files[0].File); err != nil {
		t.Fatal(err)
	}
	owner := identityOwner(t, old.Email)
	var actual map[string]any
	if err := json.Unmarshal([]byte(owner.Snapshot), &actual); err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"id": float64(original.Id), "inboundId": float64(original.InboundId), "enable": false, "email": old.Email, "up": float64(12), "down": float64(5), "allTime": float64(30), "expiryTime": float64(123456), "total": float64(999), "reset": float64(2), "monthly_reset_day": float64(12), "monthly_reset_at": float64(123), "lastOnline": float64(456)}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("incomplete persisted snapshot: got=%v want=%v", actual, expected)
	}
	if err, _ := s.CollectTraffic(identityDelta(old.Email, next.Email)); err != nil {
		t.Fatal(err)
	}
	after := identityOwner(t, old.Email)
	if after.Snapshot != owner.Snapshot || after.Up != 19 || after.Down != 5 || after.AllTime != 37 {
		t.Fatal("late settlement altered historical snapshot or lost counters", after)
	}
	// Public traffic JSON stays unchanged; the archive must not change API exposure.
	public, _ := json.Marshal(original)
	var fields map[string]any
	json.Unmarshal(public, &fields)
	if _, ok := fields["monthly_reset_day"]; ok {
		t.Fatal("archive field leaked through public API")
	}
}
