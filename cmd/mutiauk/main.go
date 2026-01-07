package main

import (
	"os"

	"github.com/coinstash/mutiauk/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
