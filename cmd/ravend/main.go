package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/PraveenC2002/raven"
)

func main() {
	
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ravend, err := raven.NewRaven(ctx); 
	if err != nil {
		log.Fatal(err)
	}

	if err := ravend.Run(); err != nil {
		log.Fatal(err)
	}
}