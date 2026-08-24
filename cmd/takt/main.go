// Command takt is the CLI entry point; all logic lives in internal/cli.
package main

import (
	"os"

	"github.com/monrad/takt/internal/cli"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, cwd))
}
