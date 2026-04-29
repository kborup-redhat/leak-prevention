package db

import "database/sql"

type CustomWatchlistDB struct {
	db *sql.DB
}

func NewCustomWatchlistDB(sqlDB *sql.DB) (*CustomWatchlistDB, error) {
	_, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS custom_watchlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			term TEXT NOT NULL UNIQUE COLLATE NOCASE,
			category TEXT NOT NULL DEFAULT 'CUSTOM',
			added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return nil, err
	}
	return &CustomWatchlistDB{db: sqlDB}, nil
}

func (c *CustomWatchlistDB) Find(term string) (Match, bool) {
	var m Match
	err := c.db.QueryRow(
		"SELECT term, category FROM custom_watchlist WHERE term = ?1 COLLATE NOCASE",
		term,
	).Scan(&m.Name, &m.Category)
	if err != nil {
		return Match{}, false
	}
	return m, true
}

func (c *CustomWatchlistDB) Add(term, category string) error {
	if category == "" {
		category = "CUSTOM"
	}
	_, err := c.db.Exec(
		"INSERT INTO custom_watchlist (term, category) VALUES (?1, ?2) ON CONFLICT (term) DO UPDATE SET category = ?2",
		term, category,
	)
	return err
}

func (c *CustomWatchlistDB) Remove(term string) error {
	_, err := c.db.Exec("DELETE FROM custom_watchlist WHERE term = ?1 COLLATE NOCASE", term)
	return err
}

func (c *CustomWatchlistDB) List() ([]CustomEntry, error) {
	rows, err := c.db.Query("SELECT term, category FROM custom_watchlist ORDER BY term")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []CustomEntry
	for rows.Next() {
		var e CustomEntry
		if err := rows.Scan(&e.Term, &e.Category); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (c *CustomWatchlistDB) Count() int {
	var count int
	_ = c.db.QueryRow("SELECT COUNT(*) FROM custom_watchlist").Scan(&count)
	return count
}

type CustomEntry struct {
	Term     string `json:"term"`
	Category string `json:"category"`
}
