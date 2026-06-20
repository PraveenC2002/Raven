package main

import (
	"log"

	"github.com/PraveenC2002/raven/internal"
)

func main() {

	ravenCLI, err := raven.NewRavenCLI()
	if err != nil {
		log.Fatal(err)
	}

	if err := ravenCLI.Run(); err != nil {
		log.Fatal(err)
	}

}
