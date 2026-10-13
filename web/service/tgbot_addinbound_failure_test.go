package service

import (
	"github.com/mymmrac/telego"
	"reflect"
	"strings"
	"testing"
	"time"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/xray"
)

func TestStage4TransactionFailureRetryAndCoreReconcile(t *testing.T) {
	for _, failure := range []string{"write", "commit", "core", "success"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, _ := stage1FakeTelegram(t)
			n.inboundService = s
			n.xrayService = &XrayService{}
			stage4CertificateFixture(t, s)
			h := commitCore(t, failure == "core")
			before, _ := s.GetInbound(1)
			beforeStat := reviewStat(t)
			db := database.GetDB()
			sqlDB, _ := db.DB()
			sqlDB.SetMaxOpenConns(1)
			if failure == "write" {
				if err := db.Exec(`CREATE TRIGGER stage4_fail BEFORE INSERT ON client_traffics BEGIN SELECT RAISE(FAIL,'injected write failure'); END`).Error; err != nil {
					t.Fatal(err)
				}
			}
			if failure == "commit" {
				for _, q := range []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE stage4_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE stage4_child(id INTEGER REFERENCES stage4_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER stage4_fail AFTER INSERT ON client_traffics BEGIN INSERT INTO stage4_child VALUES(999); END`} {
					if err := db.Exec(q).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			stage4Fill(t, n, 42, "vless", 23401, "retry")
			preview := stage3OutboundMessages[42].Text
			id := wizardSessions[42].inbound.clientID
			if strings.Contains(preview, id) || strings.Contains(preview, "key.pem") {
				t.Fatal("secret in preview")
			}
			q := stage4Rendered(t, 42, "add_inbound_confirm")
			n.answerCallback(q, true)
			after, _ := s.GetInbound(1)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeStat, reviewStat(t)) {
				t.Fatal("existing data changed")
			}
			if failure == "write" || failure == "commit" {
				var count int64
				db.Model(&model.Inbound{}).Count(&count)
				if count != 1 || h.calls.Load() != 0 {
					t.Fatal("DB failure had side effects")
				}
				db.Model(&xray.ClientTraffic{}).Count(&count)
				if count != 1 {
					t.Fatal("failed stats survived")
				}
				db.Model(&model.TrafficIdentity{}).Count(&count)
				if count != 0 {
					t.Fatal("identity side effect")
				}
				if wizardSessions[42] == nil || !strings.Contains(stage3OutboundMessages[42].Text, "未提交") {
					t.Fatal("retry owner/result missing")
				}
				n.answerCallback(q, true)
				if err := db.Exec("DROP TRIGGER stage4_fail").Error; err != nil {
					t.Fatal(err)
				}
				n.answerCallback(stage4Rendered(t, 42, "add_inbound_edit"), true)
				for _, v := range []string{"fixed", "23402", "fixed.example.com", "1", "fixed", "4", "0", "21"} {
					stage4Text(t, n, 42, v)
				}
				n.answerCallback(stage4Rendered(t, 42, "add_inbound_confirm"), true)
				_, client, err := s.GetClientByEmail("fixed")
				if err != nil || client == nil || client.TotalGB != 4*1024*1024*1024 {
					t.Fatal("retry failed")
				}
			}
			if wizardSessions[42] != nil {
				t.Fatal("committed draft retained")
			}
			texts := strings.Join(stage3OutboundTexts[42], "\n")
			if failure == "core" && !strings.Contains(texts, "核心应用失败 / 待重试") {
				t.Fatal("postcommit failure reported as success")
			}
			var count int64
			db.Model(&model.Inbound{}).Count(&count)
			if count != 2 || h.calls.Load() != 1 {
				t.Fatal("commit/core count")
			}
			n.answerCallback(q, true)
			db.Model(&model.Inbound{}).Count(&count)
			if count != 2 || h.calls.Load() != 1 {
				t.Fatal("duplicate committed twice")
			}
		})
	}
}
func TestStage4PortAndTemplateValidationActualHandler(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	stage4CertificateFixture(t, s)
	stage4Fill(t, n, 42, "vmess", 23501, "valid")
	d := wizardSessions[42].inbound
	for _, kind := range []string{"port", "protocol", "domain", "certificate", "monthly", "uuid"} {
		original := *d
		switch kind {
		case "port":
			database.GetDB().Model(&model.Inbound{}).Where("id=1").Update("port", 23501)
		case "protocol":
			d.protocol = "trojan"
		case "domain":
			d.domain = "https://bad"
		case "certificate":
			d.certificateInbound = 999
		case "monthly":
			d.monthlyDay = 32
		case "uuid":
			d.clientID = "not-a-uuid"
		}
		n.answerCallback(stage4Rendered(t, 42, "add_inbound_confirm"), true)
		if wizardSessions[42] == nil || !strings.Contains(stage3OutboundMessages[42].Text, "未提交") {
			t.Fatalf("accepted invalid %s", kind)
		}
		var count int64
		database.GetDB().Model(&model.Inbound{}).Count(&count)
		if count != 1 {
			t.Fatal("validation wrote")
		}
		*d = original
		if kind == "port" {
			database.GetDB().Model(&model.Inbound{}).Where("id=1").Update("port", 0)
		}
	}
}
func TestStage4InputActorMessageAndStaleDenial(t *testing.T) {
	s, _ := reviewDB(t, false, false)
	n, _ := stage1FakeTelegram(t)
	n.inboundService = s
	stage4CertificateFixture(t, s)
	n.answerCallback(securityQuery(42, "add_inbound"), true)
	n.answerCallback(stage4Rendered(t, 42, "add_inbound_vless"), true)
	prompt := stage3OutboundMessages[42]
	for _, actor := range []int64{7, 43} {
		n.handleTelegramMessage(telego.Message{From: &telego.User{ID: actor}, Chat: telego.Chat{ID: actor, Type: "private"}, Text: "other", ReplyToMessage: &prompt})
	}
	wrong := prompt
	wrong.MessageID++
	n.handleTelegramMessage(telego.Message{From: &telego.User{ID: 42}, Chat: prompt.Chat, Text: "wrong", ReplyToMessage: &wrong})
	if wizardSessions[42].inbound.step != 0 {
		t.Fatal("wrong input advanced")
	}
	stage4Text(t, n, 42, "right")
	n.handleTelegramMessage(telego.Message{From: &telego.User{ID: 42}, Chat: prompt.Chat, Text: "23450", ReplyToMessage: &prompt})
	if wizardSessions[42].inbound.step != 1 {
		t.Fatal("stale prompt advanced")
	}
	wizardSessions[42].until = time.Now().Add(-time.Second)
	stage4Text(t, n, 42, "23450")
	if wizardSessions[42] != nil {
		t.Fatal("expired draft retained")
	}
}
