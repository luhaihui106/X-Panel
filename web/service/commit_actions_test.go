package service

import (
	"context"
	"encoding/json"
	"github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"unsafe"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

type commitHandler struct {
	command.UnimplementedHandlerServiceServer
	calls atomic.Int32
	fail  bool
}

func (h *commitHandler) AlterInbound(context.Context, *command.AlterInboundRequest) (*command.AlterInboundResponse, error) {
	h.calls.Add(1)
	if h.fail {
		return nil, status.Error(codes.Unavailable, "injected core failure")
	}
	return &command.AlterInboundResponse{}, nil
}
func (h *commitHandler) RemoveInbound(context.Context, *command.RemoveInboundRequest) (*command.RemoveInboundResponse, error) {
	h.calls.Add(1)
	if h.fail {
		return nil, status.Error(codes.Unavailable, "injected core failure")
	}
	return &command.RemoveInboundResponse{}, nil
}
func commitCore(t *testing.T, fail bool) *commitHandler {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	h := &commitHandler{fail: fail}
	g := grpc.NewServer()
	command.RegisterHandlerServiceServer(g, h)
	go g.Serve(l)
	p = xray.NewProcess(&xray.Config{})
	v := reflect.ValueOf(p).Elem().FieldByName("apiPort")
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().SetInt(int64(l.Addr().(*net.TCPAddr).Port))
	t.Cleanup(func() { p = nil; g.Stop(); l.Close() })
	return h
}
func injectCommitFailure(t *testing.T) {
	t.Helper()
	db := database.GetDB()
	sq, _ := db.DB()
	sq.SetMaxOpenConns(2)
	for _, q := range []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE action_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE action_child(id INTEGER REFERENCES action_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER action_commit AFTER UPDATE ON client_traffics BEGIN INSERT INTO action_child VALUES(999); END`} {
		if e := db.Exec(q).Error; e != nil {
			t.Fatal(e)
		}
	}
}
func TestDepletedCleanupCommitFailureKeepsInbound(t *testing.T) {
	s, _ := reviewDB(t, true, false)
	h := commitCore(t, false)
	db := database.GetDB()
	sq, _ := db.DB()
	sq.SetMaxOpenConns(2)
	for _, q := range []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE cleanup_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE cleanup_child(id INTEGER REFERENCES cleanup_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER cleanup_commit AFTER DELETE ON inbounds BEGIN INSERT INTO cleanup_child VALUES(999); END`} {
		if e := db.Exec(q).Error; e != nil {
			t.Fatal(e)
		}
	}
	if e := s.DelDepletedClients(1); e == nil {
		t.Fatal("cleanup commit failure hidden")
	}
	if _, e := s.GetInbound(1); e != nil {
		t.Fatal("failed cleanup removed inbound")
	}
	if h.calls.Load() != 0 {
		t.Fatal("failed cleanup mutated core")
	}
}

func TestClientEditCommitFailureDoesNotMutateCore(t *testing.T) {
	s, c := reviewDB(t, true, false)
	h := commitCore(t, false)
	injectCommitFailure(t)
	c.Email = "commit-rename"
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{c}})
	if _, e := s.UpdateInboundClient(&model.Inbound{Id: 1, Settings: string(raw)}, c.ID); e == nil {
		t.Fatal("commit failure hidden")
	}
	if h.calls.Load() != 0 || reviewStat(t).Email != "review" {
		t.Fatal("uncommitted edit changed core or identity")
	}
}

func TestCoreActionsMustWaitForCommit(t *testing.T) {
	for _, kind := range []string{"renew", "client", "inbound"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := reviewDB(t, false, kind == "renew")
			h := commitCore(t, false)
			db := database.GetDB()
			if kind != "renew" {
				db.Exec(`UPDATE client_traffics SET enable=1, monthly_reset_day=0`)
			}
			if kind == "inbound" {
				db.Exec(`UPDATE inbounds SET total=1,up=2`)
			}
			before := reviewStat(t)
			injectCommitFailure(t)
			c := &trafficCollector{}
			e, r := c.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return nil, nil, nil })
			if e == nil || r || c.pending == nil {
				t.Fatalf("injection invalid: err=%v restart=%v", e, r)
			}
			if v := reviewStat(t); v.Enable != before.Enable {
				t.Fatal("db rollback invalid")
			}
			if h.calls.Load() != 0 {
				t.Fatalf("uncommitted %s mutated core: calls=%d", kind, h.calls.Load())
			}
		})
	}
}
func TestCommittedCoreFailureRequestsRestartWithoutRetryingUsage(t *testing.T) {
	s, _ := reviewDB(t, false, true)
	h := commitCore(t, true)
	c := &trafficCollector{}
	e, r := c.collect(s, func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return nil, nil, nil })
	if e != nil || !r || c.pending != nil || !reviewStat(t).Enable || h.calls.Load() != 1 {
		t.Fatalf("err=%v restart=%v pending=%v calls=%d", e, r, c.pending, h.calls.Load())
	}
}
