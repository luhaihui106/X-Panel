package controller

import (
	"encoding/json"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/entity"
	"x-ui/web/session"
	"x-ui/xray"
)

func TestImportInboundHTTPValidatesActualClients(t *testing.T) {
	for _, kind := range []string{"reserved", "mismatch", "valid"} {
		t.Run(kind, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "import.db")
			if err := database.InitDB(file); err != nil {
				t.Fatal(err)
			}
			defer database.CloseDB()
			if err := database.GetDB().Create(&model.TrafficIdentity{Kind: "client", Identity: "retired"}).Error; err != nil {
				t.Fatal(err)
			}
			email, statEmail := "fresh", "fresh"
			if kind == "reserved" {
				email = "retired"
				statEmail = "unrelated"
			}
			if kind == "mismatch" {
				statEmail = "unrelated"
			}
			settings, _ := json.Marshal(map[string]any{"clients": []model.Client{{ID: "fresh-id", Email: email, Enable: true, MonthlyResetEnabled: true, MonthlyResetDay: 12}}})
			inbound := model.Inbound{Port: 34567, Protocol: "vless", Settings: string(settings), ClientStats: []xray.ClientTraffic{{Id: 777, InboundId: 999, Email: statEmail, Up: 13, Down: 4, AllTime: 21}}}
			data, _ := json.Marshal(inbound)
			router := gin.New()
			router.Use(sessions.Sessions("test", cookie.NewStore([]byte("isolated-import-session-key"))))
			router.Use(func(c *gin.Context) { session.SetLoginUser(c, &model.User{Id: 1}); c.Next() })
			controller := InboundController{}
			router.POST("/import", controller.importInbound)
			form := url.Values{"data": {string(data)}}
			req := httptest.NewRequest("POST", "/import", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			var msg entity.Msg
			if err := json.Unmarshal(recorder.Body.Bytes(), &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Success != (kind == "valid") {
				t.Fatalf("unexpected import response: %s", recorder.Body.String())
			}
			if kind != "valid" {
				if kind == "reserved" && !strings.Contains(msg.Msg, "reserved for unsettled core counters") {
					t.Fatal(msg.Msg)
				}
				var count int64
				for _, table := range []any{&model.Inbound{}, &xray.ClientTraffic{}} {
					if err := database.GetDB().Model(table).Count(&count).Error; err != nil || count != 0 {
						t.Fatalf("rejected import persisted rows: %d %v", count, err)
					}
				}
				return
			}
			if err := database.CloseDB(); err != nil {
				t.Fatal(err)
			}
			if err := database.InitDB(file); err != nil {
				t.Fatal(err)
			}
			var saved model.Inbound
			if err := database.GetDB().Where("tag=?", "inbound-34567").First(&saved).Error; err != nil {
				t.Fatal(err)
			}
			var stat xray.ClientTraffic
			if err := database.GetDB().Where("email=?", "fresh").First(&stat).Error; err != nil {
				t.Fatal(err)
			}
			if saved.UserId != 1 || stat.InboundId != saved.Id || stat.Id == 777 || stat.Up != 13 || stat.Down != 4 || stat.AllTime != 21 || stat.MonthlyResetDay != 12 || stat.MonthlyResetAt == 0 {
				t.Fatalf("import not preserved/enrolled: inbound=%+v stats=%+v", saved, stat)
			}
		})
	}
}
