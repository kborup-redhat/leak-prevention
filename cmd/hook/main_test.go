package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatBlockResponse(t *testing.T) {
	matches := []matchEntry{
		{Name: "AWS", Parent: "Amazon", Category: "FORTUNE 500 (US)"},
		{Name: "Google Cloud", Parent: "Alphabet", Category: "FORTUNE 500 (US)"},
	}

	resp := formatBlockResponse(matches)

	var parsed struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Decision != "block" {
		t.Errorf("expected decision block, got %s", parsed.Decision)
	}
	if !strings.Contains(parsed.Reason, "leak-prevention-hook allowlist add") {
		t.Error("expected hook binary command in reason, not curl")
	}
	if strings.Contains(parsed.Reason, "curl") {
		t.Error("reason should not contain curl commands")
	}
}

func TestFormatServerDownResponse(t *testing.T) {
	resp := formatServerDownResponse()

	var parsed struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Decision != "block" {
		t.Errorf("expected decision block, got %s", parsed.Decision)
	}
}

func TestCallServer_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"blocked":true,"matches":[{"name":"AWS","parent":"Amazon","category":"FORTUNE 500 (US)"}]}`))
	}))
	defer srv.Close()

	result, err := callServer(srv.URL+"/check", "Deploy to AWS")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked {
		t.Fatal("expected blocked")
	}
}

func TestCallServer_Allowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"blocked":false,"matches":[]}`))
	}))
	defer srv.Close()

	result, err := callServer(srv.URL+"/check", "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocked {
		t.Fatal("expected not blocked")
	}
}

func TestCallServer_Unreachable(t *testing.T) {
	_, err := callServer("http://127.0.0.1:1/check", "hello")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestCmdHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","watchlist_count":3025,"alias_count":100,"custom_watchlist_count":5,"allowlist_count":2}`))
	}))
	defer srv.Close()

	code := cmdHealth(srv.Client(), srv.URL)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdHealth_Unreachable(t *testing.T) {
	code := cmdHealth(http.DefaultClient, "http://127.0.0.1:1")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCmdAllowlistAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if req["term"] != "TestTerm" {
			t.Errorf("expected term TestTerm, got %s", req["term"])
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	code := cmdAllowlistAdd(srv.Client(), srv.URL, "TestTerm")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdAllowlistList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"terms":["AWS","Google"]}`))
	}))
	defer srv.Close()

	code := cmdAllowlistList(srv.Client(), srv.URL)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdAllowlistList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"terms":[]}`))
	}))
	defer srv.Close()

	code := cmdAllowlistList(srv.Client(), srv.URL)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdAllowlistRemove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	code := cmdAllowlistRemove(srv.Client(), srv.URL, "AWS")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdWatchlistAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if req["term"] != "Acme Corp" {
			t.Errorf("expected term Acme Corp, got %s", req["term"])
		}
		if req["category"] != "CUSTOMER" {
			t.Errorf("expected category CUSTOMER, got %s", req["category"])
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	code := cmdWatchlistAdd(srv.Client(), srv.URL, "Acme Corp", "CUSTOMER")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdWatchlistAdd_NoCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if _, ok := req["category"]; ok {
			t.Error("expected no category field when empty")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	code := cmdWatchlistAdd(srv.Client(), srv.URL, "SomeTerm", "")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdWatchlistList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[{"term":"Acme Corp","category":"CUSTOMER"}]}`))
	}))
	defer srv.Close()

	code := cmdWatchlistList(srv.Client(), srv.URL)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestCmdWatchlistRemove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	code := cmdWatchlistRemove(srv.Client(), srv.URL, "Acme Corp")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestRunCLI_Help(t *testing.T) {
	code := runCLI([]string{"help"}, "http://localhost:1")
	if code != 0 {
		t.Fatalf("expected exit 0 for help, got %d", code)
	}
}

func TestRunCLI_UnknownCommand(t *testing.T) {
	code := runCLI([]string{"foobar"}, "http://localhost:1")
	if code != 1 {
		t.Fatalf("expected exit 1 for unknown command, got %d", code)
	}
}

func TestRunCLI_AllowlistNoSubcmd(t *testing.T) {
	code := runCLI([]string{"allowlist"}, "http://localhost:1")
	if code != 1 {
		t.Fatalf("expected exit 1 for missing allowlist subcommand, got %d", code)
	}
}

func TestRunCLI_WatchlistNoSubcmd(t *testing.T) {
	code := runCLI([]string{"watchlist"}, "http://localhost:1")
	if code != 1 {
		t.Fatalf("expected exit 1 for missing watchlist subcommand, got %d", code)
	}
}
