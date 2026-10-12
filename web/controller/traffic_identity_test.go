package controller

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/entity"
	"x-ui/xray"
)

func TestRetiredIdentityErrorResponsePreservesClient(t *testing.T) {
	if e := database.InitDB(filepath.Join(t.TempDir(), "ui.db")); e != nil {
		t.Fatal(e)
	}
	defer database.CloseDB()
	db := database.GetDB()
	client := model.Client{ID: "live-id", Email: "live", Enable: true}
	raw, _ := json.Marshal(map[string]any{"clients": []model.Client{client}})
	db.Create(&model.Inbound{Id: 1, Protocol: "vless", Settings: string(raw)})
	db.Create(&xray.ClientTraffic{Email: "live", InboundId: 1, Enable: true})
	db.Create(&model.TrafficIdentity{Kind: "client", Identity: "retired"})
	client.Email = "retired"
	raw, _ = json.Marshal(map[string]any{"clients": []model.Client{client}})
	router := gin.New()
	controller := InboundController{}
	router.POST("/update/:clientId", controller.updateInboundClient)
	form := url.Values{"id": {"1"}, "settings": {string(raw)}}
	req := httptest.NewRequest("POST", "/update/live-id", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var msg entity.Msg
	if e := json.Unmarshal(recorder.Body.Bytes(), &msg); e != nil {
		t.Fatal(e)
	}
	if msg.Success || !strings.Contains(msg.Msg, "reserved for unsettled core counters") || !strings.Contains(msg.Msg, "choose a new identity") {
		t.Fatalf("unsafe UI response %s", recorder.Body.String())
	}
	var stat xray.ClientTraffic
	db.First(&stat)
	if stat.Email != "live" {
		t.Fatal("error altered persisted client")
	}
	t.Log(recorder.Body.String())
}
