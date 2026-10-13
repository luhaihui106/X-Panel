package service

import (
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
)

func TestStage4TLSLeafValidityHandler(t *testing.T) {
	for _, kind := range []string{"mismatch", "future", "expired", "self-signed", "wildcard"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, _ := stage1FakeTelegram(t)
			n.inboundService = s
			now := time.Now()
			before, after := now.Add(-time.Hour), now.Add(time.Hour)
			names := []string{"example.com"}
			switch kind {
			case "mismatch":
				names = []string{"wrong.invalid"}
			case "future":
				before = now.Add(time.Minute)
			case "expired":
				after = now.Add(-time.Minute)
			case "wildcard":
				names = []string{"*.example.com"}
			}
			stage4CertificateWith(t, s, names, before, after)
			h := commitCore(t, false)
			stage4Fill(t, n, 42, "vless", 24901, "tls-leaf")
			if kind == "wildcard" {
				wizardSessions[42].inbound.domain = "one.example.com"
			}
			q := stage4Rendered(t, 42, "add_inbound_confirm")
			n.answerCallback(q, true)
			var count int64
			database.GetDB().Model(&model.Inbound{}).Count(&count)
			if kind == "self-signed" || kind == "wildcard" {
				if count != 2 || h.calls.Load() != 1 {
					t.Fatalf("valid certificate rejected count=%d core=%d", count, h.calls.Load())
				}
				return
			}
			if count != 1 || h.calls.Load() != 0 || wizardSessions[42] == nil {
				t.Fatalf("invalid leaf had side effects count=%d core=%d", count, h.calls.Load())
			}
			retry := stage4Rendered(t, 42, "add_inbound_confirm")
			if retry.Data == q.Data {
				t.Fatal("retry token not regenerated")
			}
			n.answerCallback(q, true)
			stage4CertificateWith(t, s, []string{"example.com"}, now.Add(-time.Hour), now.Add(time.Hour))
			n.answerCallback(retry, true)
			database.GetDB().Model(&model.Inbound{}).Count(&count)
			if count != 2 || h.calls.Load() != 1 || wizardSessions[42] != nil {
				t.Fatal("corrected certificate retry failed")
			}
		})
	}
}
