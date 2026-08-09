package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAdmin(t *testing.T) {
	for _, tc := range []struct {
		admin, remote string
		want          bool
	}{
		{"", "127.0.0.1:5555", true},        // an SSH tunnel lands here
		{"", "[::1]:5555", true},            //
		{"", "10.9.0.2:5555", false},        // nothing configured: peers may look, not touch
		{"10.9.0.2", "10.9.0.2:5555", true}, // the admin peer
		{"10.9.0.2", "10.9.0.3:5555", false},
		{"10.9.0.2,10.10.0.2", "10.10.0.2:5555", true}, // reachable over either tunnel
		{"10.9.0.2", "10.9.0.20:5555", false},          // no prefix matching
		{"10.9.0.2", "garbage", false},
		{"10.9.0.2", "", false},
		{"::ffff:10.9.0.2", "10.9.0.2:5555", true}, // same address, v4-mapped notation
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
		r.RemoteAddr = tc.remote
		if got := isAdmin(&Options{Admin: tc.admin}, r); got != tc.want {
			t.Errorf("-admin %q from %q: %v, want %v", tc.admin, tc.remote, got, tc.want)
		}
	}
}

// the header is attacker-controlled, so it must never widen access
func TestIsAdminIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP"} {
		r.Header.Set(h, "10.9.0.2")
		if isAdmin(&Options{Admin: "10.9.0.2"}, r) {
			t.Errorf("%s was trusted", h)
		}
		r.Header.Del(h)
	}
}

func TestRequireAdminRefuses(t *testing.T) {
	r := httptest.NewRequest(http.MethodDelete, "/api/peers/x", nil)
	r.RemoteAddr = "10.9.0.3:5555"
	w := httptest.NewRecorder()
	if requireAdmin(w, r, &Options{Admin: "10.9.0.2"}) {
		t.Fatal("let a non-admin address through")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want %d", w.Code, http.StatusForbidden)
	}
}
