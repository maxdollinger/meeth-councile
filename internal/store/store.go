// Package store owns the SQLite database behind the debate's durable records:
// each persona's private memory and the research assistant's audit log.
//
// It is the only package that talks to the database. Open creates the
// connection and applies every table; NewMemory and NewLog wrap that plain
// *sql.DB in one repository per record family, so the connection is opened once
// and each repository manages only its own logic.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

const driverName = "sqlite"

// Open opens (creating if needed) the SQLite database at path and applies the
// schema for every repository. Use ":memory:" for an ephemeral database. The
// caller owns the returned handle and closes it.
func Open(path string) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("store: path is required")
	}
	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// Records are written sequentially and SQLite serializes writers anyway, so
	// a single connection avoids SQLITE_BUSY without a retry loop.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return db, nil
}

func dsn(path string) string {
	if path == ":memory:" {
		return "file::memory:?cache=shared&_pragma=foreign_keys(1)"
	}
	return "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
