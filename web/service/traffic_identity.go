package service

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"x-ui/database/model"
	"x-ui/xray"
)

func renameInboundIdentity(tx *gorm.DB, oldTag, newTag string) error {
	if oldTag == newTag {
		return nil
	}
	if err := reserveTrafficIdentity(tx, "inbound", newTag); err != nil {
		return err
	}
	if err := tx.Model(&model.TrafficIdentity{}).Where("kind = ? AND target = ?", "inbound", oldTag).Update("target", newTag).Error; err != nil {
		return err
	}
	return tx.Create(&model.TrafficIdentity{Kind: "inbound", Identity: oldTag, Target: newTag}).Error
}

func addRetiredInboundTraffic(tx *gorm.DB, delta *xray.Traffic) error {
	if delta.Up == 0 && delta.Down == 0 {
		return nil
	}
	var owner model.TrafficIdentity
	if err := tx.Where("kind = ? AND identity = ?", "inbound", delta.Tag).First(&owner).Error; err != nil {
		return fmt.Errorf("unknown inbound traffic %q; batch retained: %w", delta.Tag, err)
	}
	if owner.Target != "" {
		return (&InboundService{}).addInboundTraffic(tx, []*xray.Traffic{{Tag: owner.Target, IsInbound: true, Up: delta.Up, Down: delta.Down}})
	}
	return tx.Model(&owner).Updates(map[string]any{"up": gorm.Expr("up + ?", delta.Up), "down": gorm.Expr("down + ?", delta.Down), "all_time": gorm.Expr("all_time + ?", delta.Up+delta.Down)}).Error
}

func retireInbound(tx *gorm.DB, inbound *model.Inbound) error {
	var stats []xray.ClientTraffic
	if err := tx.Where("inbound_id = ?", inbound.Id).Find(&stats).Error; err != nil {
		return err
	}
	for _, stat := range stats {
		if err := retireClient(tx, stat.Email); err != nil {
			return err
		}
	}
	snapshot, err := json.Marshal(inbound)
	if err != nil {
		return err
	}
	owner := model.TrafficIdentity{Kind: "inbound", Identity: inbound.Tag, Snapshot: string(snapshot), Up: inbound.Up, Down: inbound.Down, AllTime: inbound.AllTime}
	return tx.Create(&owner).Error
}

func retireClient(tx *gorm.DB, email string) error {
	var stat xray.ClientTraffic
	err := tx.Where("email = ?", email).First(&stat).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(stat)
	if err != nil {
		return err
	}
	owner := model.TrafficIdentity{Kind: "client", Identity: email, Snapshot: string(snapshot), Up: stat.Up, Down: stat.Down, AllTime: stat.AllTime}
	if err = tx.Create(&owner).Error; err != nil {
		return err
	}
	return tx.Where("email = ?", email).Delete(&xray.ClientTraffic{}).Error
}

func renameTrafficIdentity(tx *gorm.DB, oldEmail, newEmail string) error {
	if oldEmail == newEmail {
		return nil
	}
	if err := reserveTrafficIdentity(tx, "client", newEmail); err != nil {
		return err
	}
	if err := tx.Model(&model.TrafficIdentity{}).Where("kind = ? AND target = ?", "client", oldEmail).Update("target", newEmail).Error; err != nil {
		return err
	}
	return tx.Create(&model.TrafficIdentity{Kind: "client", Identity: oldEmail, Target: newEmail}).Error
}

func reserveTrafficIdentity(tx *gorm.DB, kind, identity string) error {
	var count int64
	if err := tx.Model(&model.TrafficIdentity{}).Where("kind = ? AND lower(identity) = lower(?)", kind, identity).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%s identity %q is reserved for unsettled core counters; choose a new identity", kind, identity)
	}
	return nil
}

// resolveClientTraffic returns new deltas: never mutate the retained retry batch.
func resolveClientTraffic(tx *gorm.DB, deltas []*xray.ClientTraffic) ([]*xray.ClientTraffic, error) {
	active := make([]*xray.ClientTraffic, 0, len(deltas))
	for _, delta := range deltas {
		var count int64
		if err := tx.Model(&xray.ClientTraffic{}).Where("email = ?", delta.Email).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 1 {
			return nil, fmt.Errorf("drained client identity %q has multiple live owners; batch retained", delta.Email)
		}
		if count == 1 {
			copy := *delta
			active = append(active, &copy)
			continue
		}
		if delta.Up == 0 && delta.Down == 0 {
			continue
		}
		var owner model.TrafficIdentity
		if err := tx.Where("kind = ? AND identity = ?", "client", delta.Email).First(&owner).Error; err != nil {
			return nil, fmt.Errorf("drained client identity %q has no unique owner; batch retained: %w", delta.Email, err)
		}
		if owner.Target != "" {
			copy := *delta
			copy.Email = owner.Target
			resolved, err := resolveClientTraffic(tx, []*xray.ClientTraffic{&copy})
			if err != nil {
				return nil, err
			}
			active = append(active, resolved...)
			continue
		}
		if err := tx.Model(&owner).Updates(map[string]any{"up": gorm.Expr("up + ?", delta.Up), "down": gorm.Expr("down + ?", delta.Down), "all_time": gorm.Expr("all_time + ?", delta.Up+delta.Down)}).Error; err != nil {
			return nil, err
		}
	}
	return active, nil
}
