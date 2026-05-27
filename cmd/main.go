package main

import (
	"log"
)

func main() {
	if err := bootstrap(); err != nil {
		log.Fatal(err)
	}
}