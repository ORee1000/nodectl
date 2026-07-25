package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"nodectl/internal/database"
)

func apiHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := http.StatusOK
	payload := map[string]string{"status": "ok"}

	sqlDB, err := database.GetUnderlyingDB()
	if err == nil {
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		status = http.StatusServiceUnavailable
		payload["status"] = "unhealthy"
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(payload)
	}
}
