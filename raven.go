package raven

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/joho/godotenv"
)

type ravenConfig struct {
	tgURL        string
	tgBotToken   string
	geminiAPIKey string
	tempDir      string
}

func loadRavenConfig() (*ravenConfig, error) {
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

	conf := &ravenConfig{}

	conf.tgURL = os.Getenv("TG_URL")
	if len(conf.tgURL) == 0 {
		return nil, fmt.Errorf("invalid telegram url")
	}

	conf.tgBotToken = os.Getenv("TG_BOT_TOKEN")
	if len(conf.tgBotToken) == 0 {
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

type raven struct {
	conf *ravenConfig

	db       *sql.DB
	registry Registry

	vmLockProvider *vmLockProvider
	transports     []Transport

	ctx context.Context
}

func NewRaven(ctx context.Context) (Raven, error) {
	r := &raven{
		ctx: ctx,
	}

	if err := r.bootstrap(); err != nil {
		return nil, err
	}

	return r, nil
}

func (r *raven) bootstrap() error {

	ravenConf, err := loadRavenConfig()
	if err != nil {
		return err
	}
	r.conf = ravenConf
	// .conf done

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(homeDir, ".raven", "raven.db")
	db, err := openDBPath(dbPath)
	if err != nil {
		return err
	}
	// .db done

	reg := &registry{
		db: db,
	}
	r.registry = reg
	// .registry done

	lp := newVmLockProvider(r.registry.listVm)
	r.vmLockProvider = lp
	// .vmLockProvider done

	tgUserId, err := r.registry.getUser()
	if err != nil {
		return err
	}

	tgTransp := newTgTransport(&tgTransportConf{
		userId:         *tgUserId,
		botToken:       r.conf.tgBotToken,
		vmLockProvider: r.vmLockProvider,
	}, r.conf)

	r.transports = []Transport{tgTransp}
	// .transports done

	return nil
}

func (r *raven) Run() error {

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()

	wg := sync.WaitGroup{}

	for _, t := range r.transports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer t.close()
			t.start(ctx)
		}()
	}

	wg.Wait()

	return nil
}

func (r *raven) close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}
