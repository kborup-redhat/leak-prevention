package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kborup-redhat/leak-prevention/internal/api"
	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
	_ "modernc.org/sqlite"
)

func main() {
	watchlistPath := flag.String("watchlist", "/data/watchlist.db", "Path to watchlist SQLite database")
	allowlistDir := flag.String("allowlist-dir", "/data/allowlist", "Directory for allowlist SQLite database")
	port := flag.Int("port", 8642, "Port to listen on")
	flag.Parse()

	if _, err := os.Stat(*watchlistPath); err != nil {
		log.Fatalf("Watchlist database not found: %s", *watchlistPath)
	}
	watchDB, err := sql.Open("sqlite", *watchlistPath+"?mode=ro")
	if err != nil {
		log.Fatalf("Failed to open watchlist: %v", err)
	}
	defer func() { _ = watchDB.Close() }()

	if err := os.MkdirAll(*allowlistDir, 0750); err != nil {
		log.Fatalf("Failed to create allowlist directory: %v", err)
	}
	allowlistPath := filepath.Join(*allowlistDir, "allowlist.db")
	allowDB, err := sql.Open("sqlite", allowlistPath)
	if err != nil {
		log.Fatalf("Failed to open allowlist: %v", err)
	}
	defer func() { _ = allowDB.Close() }()

	wdb := db.NewWatchlistDB(watchDB)
	adb, err := db.NewAllowlistDB(allowDB)
	if err != nil {
		log.Fatalf("Failed to initialize allowlist: %v", err)
	}

	customWatchlistPath := filepath.Join(*allowlistDir, "custom-watchlist.db")
	customDB, err := sql.Open("sqlite", customWatchlistPath)
	if err != nil {
		log.Fatalf("Failed to open custom watchlist: %v", err)
	}
	defer func() { _ = customDB.Close() }()

	cwdb, err := db.NewCustomWatchlistDB(customDB)
	if err != nil {
		log.Fatalf("Failed to initialize custom watchlist: %v", err)
	}

	m := matcher.New(wdb, adb)
	m.SetCustomWatchlist(cwdb)
	router := api.NewRouter(m, wdb, adb, cwdb)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Leak prevention server starting on %s", addr)
	log.Printf("Watchlist: %d companies, %d aliases", wdb.CompanyCount(), wdb.AliasCount())
	log.Printf("Custom watchlist: %d terms", cwdb.Count())
	log.Printf("Allowlist: %d terms", adb.Count())

	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
