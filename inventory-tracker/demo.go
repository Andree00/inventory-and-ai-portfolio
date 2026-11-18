package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"
)

func demoEnabled() bool   { return os.Getenv("DEMO_MODE") == "1" }
func demoAIEnabled() bool { return os.Getenv("DEMO_AI_ENABLED") == "1" }
func validateDemoDSN(dsn string) error {
	if !demoEnabled() {
		return nil
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return err
	}
	if cfg.DBName != "inventory_portfolio_demo" {
		return appError{500, "Demo mode requires the separate inventory_portfolio_demo database."}
	}
	return nil
}
func demoConfig(w http.ResponseWriter, r *http.Request) error {
	if e := method(r, "GET"); e != nil {
		return e
	}
	respond(w, M{"demo": demoEnabled(), "read_only": demoEnabled(), "ai_enabled": !demoEnabled() || demoAIEnabled()})
	return nil
}
func demoGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if demoEnabled() && strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if r.URL.Path == "/api/inventory-insights/explain" && demoAIEnabled() {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			message := "This portfolio demo is read-only."
			if r.URL.Path == "/api/inventory-insights/explain" {
				message = "AI generation is not enabled in this public demo."
			}
			_ = json.NewEncoder(w).Encode(M{"error": message})
			return
		}
		next.ServeHTTP(w, r)
	})
}
