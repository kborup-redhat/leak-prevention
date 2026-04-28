package db

import "database/sql"

type AllowlistDB struct {
	db *sql.DB
}

func NewAllowlistDB(sqlDB *sql.DB) (*AllowlistDB, error) {
	_, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS allowlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			term TEXT NOT NULL UNIQUE COLLATE NOCASE,
			added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return nil, err
	}
	return &AllowlistDB{db: sqlDB}, nil
}

func (a *AllowlistDB) IsAllowed(term string) bool {
	var count int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM allowlist WHERE term = ?1 COLLATE NOCASE", term).Scan(&count)
	return count > 0
}

func (a *AllowlistDB) Add(term string) error {
	_, err := a.db.Exec(
		"INSERT INTO allowlist (term) VALUES (?1) ON CONFLICT (term) DO NOTHING",
		term,
	)
	return err
}

func (a *AllowlistDB) Remove(term string) error {
	_, err := a.db.Exec("DELETE FROM allowlist WHERE term = ?1 COLLATE NOCASE", term)
	return err
}

func (a *AllowlistDB) List() ([]string, error) {
	rows, err := a.db.Query("SELECT term FROM allowlist ORDER BY term")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var terms []string
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			return nil, err
		}
		terms = append(terms, term)
	}
	return terms, rows.Err()
}

func (a *AllowlistDB) Count() int {
	var count int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM allowlist").Scan(&count)
	return count
}
