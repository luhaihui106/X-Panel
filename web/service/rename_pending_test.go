package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"unsafe"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func TestUnknownDrainedIdentityRetainsBatch(t *testing.T) {
	s, _ := reviewDB(t, true, false)
	c := &trafficCollector{}
	e, _ := c.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
		return nil, []*xray.ClientTraffic{{Email: "missing-old-email", Up: 7}}, nil
	})
	if e == nil || c.pending == nil {
		t.Fatal("unmatched drained identity silently discarded")
	}
}

func TestRunningCoreRejectsIdentityRename(t *testing.T) {
	s, c := reviewDB(t, true, false)
	commitCore(t, false)
	v := reflect.ValueOf(p).Elem().FieldByName("cmd")
	cmd := &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(cmd))
	defer reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.Zero(v.Type()))
	c.Email = "running-renamed"
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{c}})
	if _, e := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: string(raw)}, c.ID); e == nil {
		t.Fatal("running core rename accepted")
	}
	in, _ := s.GetInbound(1)
	in.Settings = string(raw)
	if _, _, e := s.UpdateInbound(in); e == nil {
		t.Fatal("running bulk rename accepted")
	}
	if reviewStat(t).Email != "review" {
		t.Fatal("refused rename changed statistics")
	}
}

func TestPendingDrainRejectsRenameUntilSuccessfulRetry(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(map[bool]string{false: "client", true: "inbound"}[bulk], func(t *testing.T) {
			s, c := reviewDB(t, true, false)
			db := database.GetDB()
			scheduledTraffic.pending = nil
			t.Cleanup(func() { scheduledTraffic.pending = nil })
			if e := db.Exec(`CREATE TRIGGER rename_delta BEFORE UPDATE ON client_traffics WHEN NEW.up > OLD.up BEGIN SELECT RAISE(FAIL,'delta'); END`).Error; e != nil {
				t.Fatal(e)
			}
			drain := func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
				return nil, []*xray.ClientTraffic{{Email: c.Email, Up: 7}}, nil
			}
			if e, _ := s.CollectTraffic(drain); e == nil {
				t.Fatal("injection invalid")
			}
			c.Email = "renamed"
			raw, _ := json.Marshal(map[string]any{"clients": []model.Client{c}})
			edit := func() error {
				if bulk {
					v, _ := s.GetInbound(1)
					v.Settings = string(raw)
					_, _, e := s.UpdateInbound(v)
					return e
				}
				_, e := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: string(raw)}, c.ID)
				return e
			}
			p = xray.NewProcess(&xray.Config{})
			t.Cleanup(func() { p = nil })
			if e := edit(); e == nil {
				t.Fatal("rename accepted with retained old identity batch")
			}
			if reviewStat(t).Email != "review" || scheduledTraffic.pending == nil {
				t.Fatal("failed rename changed identity or discarded batch")
			}
			db.Exec(`DROP TRIGGER rename_delta`)
			if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
				t.Fatal("retry must not drain again")
				return nil, nil, nil
			}); e != nil {
				t.Fatal(e)
			}
			if e := edit(); e != nil {
				t.Fatal(e)
			}
			v := reviewStat(t)
			if v.AllTime != 7 || v.Email != "renamed" {
				t.Fatalf("lost accounting: %+v", v)
			}
		})
	}
}
