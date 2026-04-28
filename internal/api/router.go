package api

import (
	"net/http"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
)

func NewRouter(m *matcher.Matcher, wdb *db.WatchlistDB, adb *db.AllowlistDB) http.Handler {
	mux := http.NewServeMux()

	h := &Handler{matcher: m, watchlist: wdb, allowlist: adb}

	mux.HandleFunc("POST /check", h.Check)
	mux.HandleFunc("GET /allowlist", h.ListAllowlist)
	mux.HandleFunc("POST /allowlist", h.AddAllowlist)
	mux.HandleFunc("DELETE /allowlist/{term}", h.DeleteAllowlist)
	mux.HandleFunc("GET /health", h.Health)

	return mux
}
