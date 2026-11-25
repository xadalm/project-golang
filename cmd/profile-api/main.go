package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"example.com/linkup/internal/demo"
	"example.com/linkup/internal/discovery"
)

type profileServer struct {
	profiles map[string]discovery.Profile
	mu       sync.RWMutex
	mode     string
	slowFor  time.Duration
}

func main() {
	addr := env("PROFILE_API_ADDR", "127.0.0.1:8081")
	slowMillis, err := strconv.Atoi(env("PROFILE_API_SLOW_MS", "2000"))
	if err != nil || slowMillis <= 0 || slowMillis > 24*60*60*1000 {
		log.Fatal("PROFILE_API_SLOW_MS must be between 1 and 86400000")
	}

	profiles := make(map[string]discovery.Profile)
	for _, profile := range demo.Profiles() {
		profiles[profile.ID] = profile
	}
	dependency := &profileServer{profiles: profiles, mode: "ok", slowFor: time.Duration(slowMillis) * time.Millisecond}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/profiles/{id}", dependency.get)
	mux.HandleFunc("PUT /_admin/mode", dependency.setMode)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      max(5*time.Second, dependency.slowFor+time.Second),
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen on %s: %v", addr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("profile API shutdown: %v", err)
			_ = server.Close()
		}
	}()

	log.Printf("Profile API listening on %s", addr)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (s *profileServer) get(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	mode := s.mode
	s.mu.RUnlock()
	switch mode {
	case "missing":
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	case "error":
		http.Error(w, "dependency failure", http.StatusInternalServerError)
		return
	case "slow":
		timer := time.NewTimer(s.slowFor)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
	case "large":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","active":true,"padding":"` + strings.Repeat("x", 8192) + `"}`))
		return
	}

	profile, ok := s.profiles[r.PathValue("id")]
	if !ok {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}{ID: profile.ID, Active: profile.Active})
}

func (s *profileServer) setMode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 128)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request struct {
		Mode string `json:"mode"`
	}
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid mode request", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "expected one JSON value", http.StatusBadRequest)
		return
	}
	switch request.Mode {
	case "ok", "missing", "error", "slow", "large":
	default:
		http.Error(w, "unknown mode", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.mode = request.Mode
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
