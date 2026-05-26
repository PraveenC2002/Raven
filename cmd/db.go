package main

import (
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

func openPath(dbPath, schemaPath string) (*sql.DB, error) {

	dbPath = "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		db.Close()
		return nil, err
	}

	if _, err = db.Exec(string(schema)); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
