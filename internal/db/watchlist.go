package db

import "database/sql"

type Match struct {
	Name     string `json:"name"`
	Parent   string `json:"parent,omitempty"`
	Category string `json:"category"`
}

type WatchlistDB struct {
	db *sql.DB
}

func NewWatchlistDB(sqlDB *sql.DB) *WatchlistDB {
	return &WatchlistDB{db: sqlDB}
}

func (w *WatchlistDB) FindCompany(term string) (Match, bool) {
	var m Match

	row := w.db.QueryRow(
		"SELECT name, category FROM companies WHERE name = ?1 COLLATE NOCASE",
		term,
	)
	if err := row.Scan(&m.Name, &m.Category); err == nil {
		return m, true
	}

	row = w.db.QueryRow(`
		SELECT a.alias, c.name, c.category
		FROM aliases a
		JOIN companies c ON a.company_id = c.id
		WHERE a.alias = ?1 COLLATE NOCASE`,
		term,
	)
	if err := row.Scan(&m.Name, &m.Parent, &m.Category); err == nil {
		return m, true
	}

	return Match{}, false
}

func (w *WatchlistDB) CompanyCount() int {
	var count int
	w.db.QueryRow("SELECT COUNT(*) FROM companies").Scan(&count)
	return count
}

func (w *WatchlistDB) AliasCount() int {
	var count int
	w.db.QueryRow("SELECT COUNT(*) FROM aliases").Scan(&count)
	return count
}
