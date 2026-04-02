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
	os.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	defer os.Unsetenv("GOOGLE_CLOUD_PROJECT")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing SLACK_SIGNING_SECRET")
	}
}

func TestLoadConfig_MissingProjectID(t *testing.T) {
	os.Setenv("SLACK_SIGNING_SECRET", "secret")
	defer os.Unsetenv("SLACK_SIGNING_SECRET")
	os.Unsetenv("GOOGLE_CLOUD_PROJECT")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing GOOGLE_CLOUD_PROJECT")
	}
}

func TestLoadConfig_DefaultLocation(t *testing.T) {
	os.Setenv("SLACK_SIGNING_SECRET", "secret")
	os.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	os.Unsetenv("CLOUD_RUN_LOCATION")
	os.Unsetenv("PORT")
	defer func() {
		os.Unsetenv("SLACK_SIGNING_SECRET")
		os.Unsetenv("GOOGLE_CLOUD_PROJECT")
	}()

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.location != "asia-northeast1" {
		t.Errorf("location = %q, want asia-northeast1", cfg.location)
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
