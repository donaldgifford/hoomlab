// Package main is the entry point for the hoomlab
package main

import (
	"fmt"
	"log/slog"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Printf("hoomlab %s (%s, %s)\n", version, commit, date)
	return nil
}
