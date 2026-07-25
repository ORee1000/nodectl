package service

import (
	"strings"
	"testing"
)

func TestValidateSubscriptionURLRejectsUnsafeTargets(t *testing.T) {
	for _, rawURL := range []string{
		"file:///etc/passwd",
		"http://127.0.0.1/sub",
		"http://[::1]/sub",
		"http://169.254.169.254/latest/meta-data",
		"http://user:pass@example.com/sub",
	} {
		if err := ValidateSubscriptionURL(rawURL); err == nil {
			t.Errorf("ValidateSubscriptionURL(%q) unexpectedly succeeded", rawURL)
		}
	}
}

func TestReadLimitedSubscriptionRejectsOversizedBody(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", maxAirportSubscriptionBytes+1))
	if _, err := readLimitedSubscription(body); err == nil {
		t.Fatal("expected oversized subscription to fail")
	}
}

func TestSanitizeDisplayName(t *testing.T) {
	got := sanitizeDisplayName(" \x00hello\nworld ")
	if got != "helloworld" {
		t.Fatalf("sanitizeDisplayName() = %q", got)
	}
	if len([]rune(sanitizeDisplayName(strings.Repeat("界", 250)))) != 200 {
		t.Fatal("display name was not capped at 200 runes")
	}
}
