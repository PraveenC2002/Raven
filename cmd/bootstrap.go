package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

type config struct {
	tgURL   string
	tgToken string
	geminiAPIKey string
	tempDir string
}

func loadConfig() (*config, error) {

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	ravenPath := filepath.Join(home, ".raven")

	err = os.MkdirAll(ravenPath, 0o755)
	if err != nil {
		return nil, err
	}
	_ = godotenv.Load(filepath.Join(ravenPath, ".env"))

	conf := &config{}

	conf.tgURL = os.Getenv("TG_URL")
	if len(conf.tgURL) == 0 {
		return nil, fmt.Errorf("invalid telegram url")
	}

	conf.tgToken = os.Getenv("TG_BOT_TOKEN")
	if len(conf.tgToken) == 0 {
		return nil, fmt.Errorf("invalid telegram token")
	}

	conf.geminiAPIKey = os.Getenv("GEMINI_API_KEY")
	if len(conf.geminiAPIKey) == 0 {
		return nil, fmt.Errorf("no gemini api key provided")
	}

	conf.tempDir, err = os.MkdirTemp("", "raven-*")
	if err != nil {
		return nil, err
	}
	
	return conf, nil
}

func bootstrap() error {

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(homeDir, ".raven", "raven.db")
	db, err := openDBPath(dbPath)

	if err != nil {
		return err
	}
	defer db.Close()

	r := &registry{
		db: db,
	}

	cmd := setupCmd(r)

	if err := cmd.Execute(); err != nil {
		return err
	}

	return nil
}
