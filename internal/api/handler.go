package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
)

type Handler struct {
	matcher   *matcher.Matcher
	watchlist *db.WatchlistDB
	allowlist *db.AllowlistDB
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	result := h.matcher.Check(req.Prompt)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("failed to encode check response: %v", err)
	}
}

func (h *Handler) ListAllowlist(w http.ResponseWriter, r *http.Request) {
	terms, err := h.allowlist.List()
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if terms == nil {
		terms = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string][]string{"terms": terms}); err != nil {
		log.Printf("failed to encode allowlist response: %v", err)
	}
}

func (h *Handler) AddAllowlist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Term string `json:"term"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Term == "" {
		http.Error(w, `{"error":"invalid request, need {\"term\":\"...\"}"}`, http.StatusBadRequest)
		return
	}
	if err := h.allowlist.Add(req.Term); err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) DeleteAllowlist(w http.ResponseWriter, r *http.Request) {
	term := r.PathValue("term")
	if term == "" {
		http.Error(w, `{"error":"term required"}`, http.StatusBadRequest)
		return
	}
	if err := h.allowlist.Remove(term); err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"status":          "ok",
		"watchlist_count": h.watchlist.CompanyCount(),
		"alias_count":     h.watchlist.AliasCount(),
		"allowlist_count": h.allowlist.Count(),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to encode health response: %v", err)
	}
}
