package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodectl/internal/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAPIHealth(t *testing.T) {
	oldDB := database.DB
	t.Cleanup(func() {
		database.DB = oldDB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	database.DB = db

	req := httptest.NewRequest(http.MethodGet, "/api/public/health", nil)
	rec := httptest.NewRecorder()
	apiHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestAPIHealthRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/public/health", nil)
	rec := httptest.NewRecorder()
	apiHealth(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
