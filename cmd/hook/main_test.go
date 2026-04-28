package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if parsed.Reason == "" {
		t.Error("expected non-empty reason")
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
