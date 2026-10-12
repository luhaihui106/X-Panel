package service

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"time"
	"x-ui/database/model"
	"x-ui/xray"
)

// Preserve omitted downstream fields from older API clients, but respect explicit values.
func preserveMonthlyFields(oldJSON, newJSON string) (string, error) {
	var oldSettings, newSettings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(oldJSON), &oldSettings); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(newJSON), &newSettings); err != nil {
		return "", err
	}
	if len(oldSettings["clients"]) == 0 {
		oldSettings["clients"] = json.RawMessage("[]")
	}
	if len(newSettings["clients"]) == 0 {
		return newJSON, nil
	}
	var oldClients, newClients []map[string]json.RawMessage
	if err := json.Unmarshal(oldSettings["clients"], &oldClients); err != nil {
		return "", err
	}
	if err := json.Unmarshal(newSettings["clients"], &newClients); err != nil {
		return "", err
	}
	for _, next := range newClients {
		for _, previous := range oldClients {
			same := false
			for _, key := range []string{"id", "email", "password"} {
				var a, b string
				json.Unmarshal(next[key], &a)
				json.Unmarshal(previous[key], &b)
				if a != "" && a == b {
					same = true
					break
				}
			}
			if !same {
				continue
			}
			for _, key := range []string{"monthlyResetEnabled", "monthlyResetDay"} {
				if _, ok := next[key]; !ok {
					if value, exists := previous[key]; exists {
						next[key] = value
					}
				}
			}
			break
		}
	}
	raw, err := json.Marshal(newClients)
	if err != nil {
		return "", err
	}
	newSettings["clients"] = raw
	raw, err = json.Marshal(newSettings)
	return string(raw), err
}

// monthlyEnrollment uses the same persisted panel timezone as the scheduler.
func monthlyEnrollment(tx *gorm.DB, client *model.Client) (int, int64, error) {
	if !client.MonthlyResetEnabled {
		return 0, 0, nil
	}
	if client.MonthlyResetDay < 1 || client.MonthlyResetDay > 31 {
		return 0, 0, fmt.Errorf("monthlyResetDay must be 1..31")
	}
	var setting model.Setting
	result := tx.Where("key = ?", "timeLocation").Find(&setting)
	if result.Error != nil {
		return 0, 0, result.Error
	}
	zone := setting.Value
	if zone == "" {
		zone = "Local"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return 0, 0, err
	}
	return client.MonthlyResetDay, monthlyBoundary(time.Now().In(loc), client.MonthlyResetDay).UnixMilli(), nil
}

// Called only after core counters have been drained and applied in AddTraffic.
// The cycle marker and traffic reset share the caller's SQLite transaction.
func (s *InboundService) resetMonthlyClients(tx *gorm.DB, now time.Time) (bool, error) {
	var inbounds []model.Inbound
	if err := tx.Find(&inbounds).Error; err != nil {
		return false, err
	}
	restart := false
	for _, inbound := range inbounds {
		clients, err := s.GetClients(&inbound)
		if err != nil {
			return false, err
		}
		for _, client := range clients {
			var stat xray.ClientTraffic
			result := tx.Where("inbound_id = ? AND email = ?", inbound.Id, client.Email).Find(&stat)
			if result.Error != nil {
				return false, result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			if !client.MonthlyResetEnabled {
				if stat.MonthlyResetDay != 0 {
					if err := tx.Model(&stat).Updates(map[string]any{"monthly_reset_day": 0, "monthly_reset_at": 0}).Error; err != nil {
						return false, err
					}
				}
				continue
			}
			if client.MonthlyResetDay < 1 || client.MonthlyResetDay > 31 {
				continue
			}
			boundary := monthlyBoundary(now, client.MonthlyResetDay).UnixMilli()
			if stat.MonthlyResetDay != client.MonthlyResetDay || stat.MonthlyResetAt == 0 {
				if err := tx.Model(&stat).Updates(map[string]any{"monthly_reset_day": client.MonthlyResetDay, "monthly_reset_at": boundary}).Error; err != nil {
					return false, err
				}
				continue
			}
			if stat.MonthlyResetAt >= boundary {
				continue
			}
			enable := stat.Enable
			valid := client.Enable && (client.ExpiryTime <= 0 || client.ExpiryTime > now.UnixMilli()) && (stat.ExpiryTime <= 0 || stat.ExpiryTime > now.UnixMilli())
			// Recover only traffic-quota disabled clients; never manual or expired ones.
			if !valid {
				enable = false
			} else if !enable && stat.Total > 0 && stat.Up+stat.Down >= stat.Total {
				enable = true
				restart = true
			}
			if err := tx.Model(&stat).Updates(map[string]any{"up": 0, "down": 0, "enable": enable, "monthly_reset_at": boundary}).Error; err != nil {
				return false, err
			}
		}
	}
	return restart, nil
}

// monthlyBoundary returns the latest scheduled midnight in the panel location.
func monthlyBoundary(now time.Time, day int) time.Time {
	boundary := func(year int, month time.Month) time.Time {
		last := time.Date(year, month+1, 0, 0, 0, 0, 0, now.Location()).Day()
		d := day
		if d > last {
			d = last
		}
		return time.Date(year, month, d, 0, 0, 0, 0, now.Location())
	}
	current := boundary(now.Year(), now.Month())
	if now.Before(current) {
		return boundary(now.Year(), now.Month()-1)
	}
	return current
}
