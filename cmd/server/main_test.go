package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLoadConfig_MissingSigningSecret(t *testing.T) {
	os.Unsetenv("SLACK_SIGNING_SECRET")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing SLACK_SIGNING_SECRET")
	}
}

func TestLoadConfig_DefaultPort(t *testing.T) {
	os.Setenv("SLACK_SIGNING_SECRET", "secret")
	os.Unsetenv("PORT")
	defer os.Unsetenv("SLACK_SIGNING_SECRET")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != "8080" {
		t.Errorf("port = %q, want 8080", cfg.port)
	}
}

func TestHealthzEndpoint(t *testing.T) {
	// given
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// when
	resp, err := http.Get(srv.URL + "/healthz")

	// then
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
