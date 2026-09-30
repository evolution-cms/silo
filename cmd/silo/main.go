package main

import (
	"os"

	"github.com/evolution-cms/silo/internal/cli"
)

// Set with -ldflags at release build time.
var version = "0.1.0-dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version, os.Stdin, os.Stdout, os.Stderr))
}
