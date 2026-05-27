package main

import (
	"database/sql"
	// "os"

	_ "embed"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

func openPath(dbPath string) (*sql.DB, error) {

	dbPath = "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	// schema, err := os.ReadFile(schemaSQL)
	// if err != nil {
	// 	db.Close()
	// 	return nil, err
	// }

	if _, err = db.Exec(string(schemaSQL)); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
