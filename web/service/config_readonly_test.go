package service

import (
	"testing"
	"x-ui/database"
	"x-ui/database/model"
)

func TestMonthlyConfigGenerationIsReadOnly(t *testing.T) {
	_, _ = reviewDB(t, true, true)
	before := reviewStat(t)
	if err := database.GetDB().Create(&model.Setting{Key: "xrayTemplateConfig", Value: `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&XrayService{}).GetXrayConfig(); err != nil {
		t.Fatal(err)
	}
	after := reviewStat(t)
	if before.Up != after.Up || before.ExpiryTime != after.ExpiryTime || before.MonthlyResetAt != after.MonthlyResetAt {
		t.Fatalf("config generation mutated stats: before=%+v after=%+v", before, after)
	}
}
