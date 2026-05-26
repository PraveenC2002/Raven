package main

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type config struct {
	tgURL string
	tgToken string
}

func loadConf() (*config, error) {
	err := godotenv.Load()
	if err != nil {
		return nil, err
	}

	conf := &config{}
	
	conf.tgURL = os.Getenv("TG_URL")
	if len(conf.tgURL) == 0 {
		return nil, fmt.Errorf("invalid telegram url")
	}
	
	conf.tgToken = os.Getenv("TG_BOT_TOKEN")
	if len(conf.tgToken) == 0 {
		return nil, fmt.Errorf("invalid telegram token")
	}

	
	return conf, nil
}