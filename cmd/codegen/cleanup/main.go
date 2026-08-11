package main

import (
	"log"
	"os"

	"github.com/rancher/wrangler/v3/pkg/cleanup"
)

func main() {
	if err := cleanup.Cleanup("./pkg/apis"); err != nil {
		log.Fatal(err)
	}
	if err := os.RemoveAll("./pkg/generated"); err != nil {
		log.Fatal(err)
	}
}
