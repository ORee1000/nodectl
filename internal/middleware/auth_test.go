package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func resetTrustedProxyConfigForTest(t *testing.T, value string) {
	t.Helper()
	old, existed := os.LookupEnv("NODECTL_TRUSTED_PROXIES")
	if value == "" {
		os.Unsetenv("NODECTL_TRUSTED_PROXIES")
	} else {
		os.Setenv("NODECTL_TRUSTED_PROXIES", value)
	}
	trustedProxyOnce = sync.Once{}
	trustedProxyNets = nil
	t.Cleanup(func() {
		if existed {
			os.Setenv("NODECTL_TRUSTED_PROXIES", old)
		} else {
			os.Unsetenv("NODECTL_TRUSTED_PROXIES")
		}
		trustedProxyOnce = sync.Once{}
		trustedProxyNets = nil
	})
}

func TestGetClientIPIgnoresUntrustedForwardedHeaders(t *testing.T) {
	resetTrustedProxyConfigForTest(t, "")
	req := httptest.NewRequest("GET", "http://example.test/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Real-IP", "198.51.100.20")
	req.Header.Set("X-Forwarded-For", "198.51.100.21")

	if got := getClientIP(req); got != "203.0.113.9" {
		t.Fatalf("getClientIP() = %q, want direct peer", got)
	}
	if RequestIsHTTPS(req) {
		t.Fatal("untrusted X-Forwarded-Proto must not enable HTTPS")
	}
}

func TestTrustedProxyHeaders(t *testing.T) {
	resetTrustedProxyConfigForTest(t, "")
	req := httptest.NewRequest("GET", "http://example.test/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.21, 127.0.0.1")
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := getClientIP(req); got != "198.51.100.21" {
		t.Fatalf("getClientIP() = %q", got)
	}
	if !RequestIsHTTPS(req) {
		t.Fatal("trusted proxy HTTPS header was ignored")
	}
}

func TestForceHTTPSBlocksRemotePlainHTTP(t *testing.T) {
	resetTrustedProxyConfigForTest(t, "")
	t.Setenv("NODECTL_ALLOW_INSECURE_HTTP", "")
	nextCalled := false
	handler := ForceHTTPS(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "http://example.test/login", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()

	handler(rec, req)
	if nextCalled {
		t.Fatal("plain remote HTTP request was allowed")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUpgradeRequired)
	}
}

func TestForceHTTPSAllowsLoopbackSetup(t *testing.T) {
	resetTrustedProxyConfigForTest(t, "")
	handler := ForceHTTPS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "http://localhost/login", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()

	handler(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
