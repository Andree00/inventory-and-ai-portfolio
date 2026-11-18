package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDemoReadOnlyGuard(t *testing.T) {
	t.Setenv("DEMO_MODE", "1")
	t.Setenv("DEMO_AI_ENABLED", "0")
	h := demoGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/items", 204},
		{"POST", "/api/documents", 403},
		{"PUT", "/api/items", 403},
		{"DELETE", "/api/items", 403},
		{"POST", "/api/inventory-insights/explain", 403},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
		if rec.Code != tt.want {
			t.Fatalf("%s %s: got %d, want %d", tt.method, tt.path, rec.Code, tt.want)
		}
	}
	t.Setenv("DEMO_AI_ENABLED", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/inventory-insights/explain", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("enabled AI: %d", rec.Code)
	}
}

func TestDemoRequiresSeparateDatabase(t *testing.T) {
	t.Setenv("DEMO_MODE", "1")
	if validateDemoDSN("user:password@tcp(localhost:3306)/inventory_portfolio") == nil {
		t.Fatal("normal inventory database must be rejected")
	}
	if err := validateDemoDSN("user:password@tcp(localhost:3306)/inventory_portfolio_demo"); err != nil {
		t.Fatal(err)
	}
}
