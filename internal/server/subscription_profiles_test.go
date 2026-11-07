package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nodectl/internal/database"
	"nodectl/internal/logger"
	"nodectl/internal/service"

	"github.com/glebarez/sqlite"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupSubscriptionTest(t *testing.T) {
	t.Helper()
	oldDB, oldLog := database.DB, logger.Log
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	database.DB, logger.Log = db, slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { database.DB, logger.Log = oldDB, oldLog; sqlDB.Close() })
	if err := db.AutoMigrate(&database.SysConfig{}, &database.SubscriptionProfile{}, &database.NodePool{}, &database.AirportNode{}, &database.CustomNode{}); err != nil {
		t.Fatal(err)
	}
	for _, config := range []database.SysConfig{{Key: "sub_token", Value: "legacy-token"}, {Key: "sub_custom_name", Value: "Legacy"}} {
		if err := db.Create(&config).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func subscriptionCall(handler http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = strings.NewReader(string(data))
	}
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(method, path, reader))
	return rec
}

func createTestProfile(t *testing.T, name string, all bool, keys ...string) database.SubscriptionProfile {
	t.Helper()
	rec := subscriptionCall(apiSubscriptions, "POST", "/api/subscriptions", map[string]any{"name": name, "enabled": true, "all_landing_nodes": all, "landing_node_keys": keys})
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Data database.SubscriptionProfile `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Token) != 64 || result.Data.ID == "" {
		t.Fatal("missing generated ID/token")
	}
	return result.Data
}

func seedSubscriptionNodes(t *testing.T) []string {
	t.Helper()
	for _, n := range []database.NodePool{
		{UUID: "relay", Name: "relay", RoutingType: 1, IPV4: "192.0.2.1", Links: map[string]string{"trojan": "trojan://relay-secret@192.0.2.1:443"}, TrafficLimit: 100, TrafficUp: 1},
		{UUID: "shared", Name: "shared", RoutingType: 2, IPV4: "192.0.2.2", Links: map[string]string{"trojan": "trojan://shared-secret@192.0.2.2:443"}, TrafficLimit: 200, TrafficUp: 2},
		{UUID: "private", Name: "private", RoutingType: 2, IPV4: "192.0.2.3", Links: map[string]string{"trojan": "trojan://private-secret@192.0.2.3:443"}, TrafficLimit: 400, TrafficUp: 4},
		{UUID: "blocked", Name: "blocked", RoutingType: 2, IsBlocked: true, IPV4: "192.0.2.4", Links: map[string]string{"trojan": "trojan://blocked-secret@192.0.2.4:443"}},
	} {
		if err := database.DB.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	a := database.AirportNode{ID: "airport-shared", SubID: "source", Name: "airport-shared", RoutingType: 2, Link: "trojan://airport-shared-secret@192.0.2.5:443#airport-shared"}
	for _, n := range []database.AirportNode{a, {ID: "airport-private", SubID: "source", Name: "airport-private", RoutingType: 2, Link: "trojan://airport-private-secret@192.0.2.6:443#airport-private"}} {
		if err := database.DB.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []database.CustomNode{
		{ID: "custom-shared", Name: "custom-shared", RoutingType: 2, Link: "trojan://custom-shared-secret@192.0.2.7:443#custom-shared"},
		{ID: "custom-private", Name: "custom-private", RoutingType: 2, Link: "trojan://custom-private-secret@192.0.2.8:443#custom-private"},
	} {
		if err := database.DB.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	return []string{"node:shared", service.AirportSubscriptionKey(a), "custom:custom-shared"}
}

func TestSubscriptionsIsolateEveryNodeSourceAndFormat(t *testing.T) {
	setupSubscriptionTest(t)
	keys := seedSubscriptionNodes(t)
	a := createTestProfile(t, "Share A", false, keys...)
	b := createTestProfile(t, "Private B", true)
	empty := createTestProfile(t, "Empty", false)
	if a.Token == b.Token {
		t.Fatal("tokens must differ")
	}
	for _, tc := range []struct {
		name, token     string
		shared, private bool
	}{
		{"A", a.Token, true, false}, {"B", b.Token, true, true}, {"empty", empty.Token, false, false}, {"legacy", "legacy-token", true, true},
	} {
		for _, format := range []string{"raw/2", "v2ray"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				handler := apiSubRaw
				if format == "v2ray" {
					handler = apiSubV2ray
				}
				// Caller-supplied profile/scope parameters must never widen token permissions.
				rec := subscriptionCall(handler, "GET", "/sub/"+format+"?token="+tc.token+"&all_landing_nodes=true&id="+b.ID, nil)
				if rec.Code != 200 {
					t.Fatal(rec.Body.String())
				}
				body := rec.Body.String()
				if format == "v2ray" {
					decoded, err := base64.StdEncoding.DecodeString(body)
					if err != nil {
						t.Fatal(err)
					}
					body = string(decoded)
				}
				for _, source := range []string{"", "airport-", "custom-"} {
					if strings.Contains(body, source+"shared-secret") != tc.shared {
						t.Errorf("shared selection mismatch: %s", body)
					}
					if strings.Contains(body, source+"private-secret") != tc.private {
						t.Errorf("private selection mismatch: %s", body)
					}
				}
				if strings.Contains(body, "blocked-secret") {
					t.Fatal("blocked node leaked")
				}
				if format == "v2ray" && !strings.Contains(body, "relay-secret") {
					t.Fatal("shared relay missing")
				}
				if format == "v2ray" && tc.name == "A" && rec.Header().Get("Subscription-Userinfo") != "upload=3; download=0; total=300" {
					t.Fatalf("private traffic leaked: %s", rec.Header())
				}
			})
		}
	}
	// Airport refresh replaces row IDs, but identical source/link retains selection.
	var airport database.AirportNode
	database.DB.First(&airport, "id = ?", "airport-shared")
	database.DB.Delete(&airport)
	airport.ID = "refreshed-id"
	if err := database.DB.Create(&airport).Error; err != nil {
		t.Fatal(err)
	}
	rec := subscriptionCall(apiSubRaw, "GET", "/sub/raw/2?token="+a.Token, nil)
	if !strings.Contains(rec.Body.String(), "airport-shared-secret") {
		t.Fatal("airport selection lost on refresh")
	}
	// A newly added landing node is excluded from fixed selection, included in all mode.
	n := database.CustomNode{ID: "future", Name: "future", RoutingType: 2, Link: "trojan://future-secret@192.0.2.9:443"}
	if err := database.DB.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	for _, p := range []database.SubscriptionProfile{a, b} {
		rec := subscriptionCall(apiSubRaw, "GET", "/sub/raw/2?token="+p.Token, nil)
		if strings.Contains(rec.Body.String(), "future-secret") != p.AllLandingNodes {
			t.Fatal("new node scope mismatch")
		}
	}
	// Empty selections remain empty after editing an existing profile.
	a.LandingNodeKeys = []string{}
	rec = subscriptionCall(apiSubscriptions, "PUT", "/api/subscriptions", a)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = subscriptionCall(apiSubRaw, "GET", "/sub/raw/2?token="+a.Token, nil)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("empty selection reverted to all")
	}
}

func TestSubscriptionLifecycleAndValidation(t *testing.T) {
	setupSubscriptionTest(t)
	profile := createTestProfile(t, "A", false)
	for _, body := range []map[string]any{{"name": ""}, {"name": "bad\nname"}, {"name": "A", "landing_node_keys": []string{"node:missing"}}, {"name": strings.Repeat("长", 101)}} {
		if rec := subscriptionCall(apiSubscriptions, "POST", "/api/subscriptions", body); rec.Code != 400 {
			t.Fatalf("invalid request accepted: %s", rec.Body.String())
		}
	}
	for _, token := range []string{"", "missing"} {
		assertSubscriptionDenied(t, token)
	}
	profile.Enabled = false
	rec := subscriptionCall(apiSubscriptions, "PUT", "/api/subscriptions", profile)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	assertSubscriptionDenied(t, profile.Token)
	profile.Enabled = true
	rec = subscriptionCall(apiSubscriptions, "PUT", "/api/subscriptions", profile)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = subscriptionCall(apiResetSubscriptionToken, "POST", "/api/subscriptions/reset-token", map[string]string{"id": profile.ID})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var reset struct {
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reset)
	if reset.Token == profile.Token || len(reset.Token) != 64 {
		t.Fatal("token was not rotated")
	}
	assertSubscriptionDenied(t, profile.Token)
	if !verifySubToken(httptest.NewRequest("GET", "/sub/raw/2?token="+reset.Token, nil)) {
		t.Fatal("new token rejected")
	}
	// Stale edit must not restore an old token.
	profile.Name = "Renamed"
	rec = subscriptionCall(apiSubscriptions, "PUT", "/api/subscriptions", profile)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	assertSubscriptionDenied(t, profile.Token)
	rec = subscriptionCall(apiSubscriptions, "DELETE", "/api/subscriptions?id="+profile.ID, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	assertSubscriptionDenied(t, reset.Token)
	rec = subscriptionCall(apiSubscriptions, "PUT", "/api/subscriptions", profile)
	if rec.Code != 404 {
		t.Fatal("deleted subscription resurrected")
	}
	if !verifySubToken(httptest.NewRequest("GET", "/sub/raw/2?token=legacy-token", nil)) {
		t.Fatal("legacy compatibility lost")
	}
	// Database errors must fail closed even if the global legacy token is valid.
	database.DB.Migrator().DropTable(&database.SubscriptionProfile{})
	if verifySubToken(httptest.NewRequest("GET", "/sub/raw/2?token=legacy-token", nil)) {
		t.Fatal("database error failed open")
	}
}

func assertSubscriptionDenied(t *testing.T, token string) {
	t.Helper()
	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/sub/clash", apiSubClash}, {"/sub/v2ray", apiSubV2ray}, {"/sub/raw/1", apiSubRaw}, {"/sub/raw/2", apiSubRaw}, {"/sub/rules/direct", apiSubRuleList},
	} {
		rec := subscriptionCall(endpoint.handler, "GET", endpoint.path+"?token="+url.QueryEscape(token), nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s accepted revoked/invalid token: %d", endpoint.path, rec.Code)
		}
	}
}

func TestClashProvidersKeepSubscriptionScopeAndSeparateCaches(t *testing.T) {
	setupSubscriptionTest(t)
	seedSubscriptionNodes(t)
	a, b := createTestProfile(t, "Share A", false, "node:shared"), createTestProfile(t, "Private B", true)
	paths := make(map[string]bool)
	for _, profile := range []database.SubscriptionProfile{a, b} {
		rec := subscriptionCall(apiSubClash, "GET", "/sub/clash?token="+profile.Token, nil)
		if rec.Code != 200 || rec.Header().Get("profile-title") != profile.Name {
			t.Fatalf("Clash failed: %s", rec.Body.String())
		}
		var config struct {
			Providers map[string]struct {
				URL  string `yaml:"url"`
				Path string `yaml:"path"`
			} `yaml:"proxy-providers"`
		}
		if err := yaml.Unmarshal(rec.Body.Bytes(), &config); err != nil {
			t.Fatal(err)
		}
		if len(config.Providers) != 2 {
			t.Fatalf("expected 2 providers: %s", rec.Body.String())
		}
		for name, provider := range config.Providers {
			u, err := url.Parse(provider.URL)
			if err != nil {
				t.Fatal(err)
			}
			if u.Query().Get("token") != profile.Token {
				t.Fatal("provider lost scoped token")
			}
			if paths[provider.Path] {
				t.Fatal("provider cache shared between subscriptions")
			}
			paths[provider.Path] = true
			raw := subscriptionCall(apiSubRaw, "GET", provider.URL, nil)
			if raw.Code != 200 {
				t.Fatal(raw.Body.String())
			}
			if name == "落地机场" && strings.Contains(raw.Body.String(), "private-secret") != profile.AllLandingNodes {
				t.Fatal("nested provider leaked private node")
			}
		}
	}
}

func TestSubscriptionMetadataAndManagementAuthentication(t *testing.T) {
	setupSubscriptionTest(t)
	seedSubscriptionNodes(t)
	createTestProfile(t, "A", false, "node:shared")
	rec := subscriptionCall(apiSubscriptions, "GET", "/api/subscriptions", nil)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "trojan://") {
		t.Fatal("node credentials exposed in selector")
	}
	for _, handler := range []http.HandlerFunc{apiSubscriptions, apiResetSubscriptionToken} {
		rec := subscriptionCall(withAuthAndSecure(handler), "POST", "https://example.test/api/subscriptions", map[string]string{"name": "unauthorized"})
		if rec.Code == 200 {
			t.Fatal("management accepted unauthenticated request")
		}
	}
	for _, method := range []string{"PATCH", "OPTIONS"} {
		rec := subscriptionCall(apiSubscriptions, method, "/api/subscriptions", nil)
		if rec.Code != 405 {
			t.Fatal(fmt.Sprintf("unsupported method: %d", rec.Code))
		}
	}
}
