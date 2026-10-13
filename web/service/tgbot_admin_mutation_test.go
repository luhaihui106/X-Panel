package service

import (
	"strings"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
)

func TestAdminSQLiteMutationActualDispatch(t *testing.T) {
	cases := []struct {
		action string
		verify func(*testing.T, *InboundService)
	}{
		{"reset_traffic_c review", func(t *testing.T, s *InboundService) {
			if reviewStat(t).Up != 0 {
				t.Fatal("traffic not reset")
			}
		}},
		{"reset_all_traffics_c", func(t *testing.T, s *InboundService) {
			if reviewStat(t).Up != 0 {
				t.Fatal("all traffic not reset")
			}
		}},
		{"limit_traffic_c review 2", func(t *testing.T, s *InboundService) {
			_, c, _ := s.GetClientByEmail("review")
			if c.TotalGB != 2*1024*1024*1024 {
				t.Fatal("quota not updated")
			}
		}},
		{"reset_exp_c review 2", func(t *testing.T, s *InboundService) {
			_, c, _ := s.GetClientByEmail("review")
			if c.ExpiryTime == 0 {
				t.Fatal("expiry not updated")
			}
		}},
		{"ip_limit_c review 2", func(t *testing.T, s *InboundService) {
			_, c, _ := s.GetClientByEmail("review")
			if c.LimitIP != 2 {
				t.Fatal("IP limit not updated")
			}
		}},
		{"clear_ips_c review", func(t *testing.T, s *InboundService) {
			ips, err := s.GetInboundClientIps("review")
			if err != nil || ips != "" {
				t.Fatal("IPs not cleared")
			}
		}},
		{"tgid_remove_c review", func(t *testing.T, s *InboundService) {
			_, c, _ := s.GetClientByEmail("review")
			if c.TgID != 0 {
				t.Fatal("binding not removed")
			}
		}},
		{"toggle_enable_c review", func(t *testing.T, s *InboundService) {
			_, c, _ := s.GetClientByEmail("review")
			if c.Enable {
				t.Fatal("enable not toggled")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			s, _ := reviewDB(t, false, false)
			n, methods := stage1FakeTelegram(t)
			n.inboundService = s
			n.xrayService = &XrayService{}
			n.settingService = &SettingService{}
			oldP := p
			p = nil
			t.Cleanup(func() { p = oldP })
			if err := database.GetDB().Create(&model.InboundClientIps{ClientEmail: "review", Ips: "[\"127.0.0.1\"]"}).Error; err != nil {
				t.Fatal(err)
			}
			before := reviewStat(t)
			if strings.HasPrefix(c.action, "tgid_remove_c") {
				if _, err := s.SetClientTelegramUserID(before.Id, 888); err != nil {
					t.Fatal(err)
				}
			}
			before = reviewStat(t)
			n.answerCallback(securityQuery(7, c.action), true)
			if reviewStat(t) != before || strings.Join(*methods, ",") != "answerCallbackQuery" {
				t.Fatal("nonadmin mutated SQLite")
			}
			*methods = nil
			n.answerCallback(securityQuery(42, c.action), false)
			if len(*methods) == 0 {
				t.Fatal("actual dispatch produced no Telegram output")
			}
			c.verify(t, s)
		})
	}
}
