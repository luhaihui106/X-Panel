package service

import (
	"context"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"net"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

type retirementStats struct {
	stats.UnimplementedStatsServiceServer
	calls int
}

func (s *retirementStats) QueryStats(_ context.Context, req *stats.QueryStatsRequest) (*stats.QueryStatsResponse, error) {
	s.calls++
	if !req.Reset_ {
		panic("non-destructive drain")
	}
	response := &stats.QueryStatsResponse{}
	if s.calls == 1 {
		response.Stat = []*stats.Stat{{Name: "user>>>review>>>traffic>>>uplink", Value: 7}, {Name: "user>>>second>>>traffic>>>uplink", Value: 3}}
	} else if s.calls == 2 {
		response.Stat = []*stats.Stat{{Name: "user>>>second>>>traffic>>>uplink", Value: 4}}
	}
	return response, nil
}
func TestIdentityRealGRPCDrainAndMonthlyReset(t *testing.T) {
	s, old, next := identityPair(t)
	commitCore(t, false)
	if _, e := s.DelInboundClient(1, old.ID); e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	g := grpc.NewServer()
	server := &retirementStats{}
	stats.RegisterStatsServiceServer(g, server)
	go g.Serve(l)
	defer g.Stop()
	defer l.Close()
	var api xray.XrayAPI
	if e := api.Init(l.Addr().(*net.TCPAddr).Port); e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return api.GetTraffic(true) }); e != nil {
		t.Fatal(e)
	}
	if v := identityOwner(t, old.Email); v.AllTime != 7 {
		t.Fatalf("old7 missing %+v", v)
	}
	db := database.GetDB()
	var healthy xray.ClientTraffic
	db.Where("email=?", next.Email).First(&healthy)
	if healthy.AllTime != 3 {
		t.Fatalf("new3 missing %+v", healthy)
	}
	in, _ := s.GetInbound(1)
	next.MonthlyResetEnabled = true
	next.MonthlyResetDay = time.Now().UTC().Day()
	raw := `{"clients":[{"id":"second","email":"second","enable":true,"monthlyResetEnabled":true,"monthlyResetDay":` + time.Now().UTC().Format("2") + `}]}`
	in.Settings = raw
	db.Save(in)
	db.Model(&xray.ClientTraffic{}).Where("email=?", next.Email).Updates(map[string]any{"monthly_reset_day": next.MonthlyResetDay, "monthly_reset_at": time.Now().AddDate(0, -1, 0).UnixMilli()})
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return api.GetTraffic(true) }); e != nil {
		t.Fatal(e)
	}
	db.Where("email=?", next.Email).First(&healthy)
	if healthy.AllTime != 7 || healthy.Up != 0 || healthy.MonthlyResetAt == 0 {
		t.Fatalf("healthy reset/usage %+v", healthy)
	}
	if e, _ := s.CollectTraffic(func() ([]*xray.Traffic, []*xray.ClientTraffic, error) { return api.GetTraffic(true) }); e != nil {
		t.Fatal(e)
	}
	db.Where("email=?", next.Email).First(&healthy)
	if healthy.AllTime != 7 {
		t.Fatal("repeated delta")
	}
	var n int64
	db.Model(&model.TrafficIdentity{}).Where("kind='client'").Count(&n)
	if n != 1 {
		t.Fatalf("duplicate ownership %d", n)
	}
}
