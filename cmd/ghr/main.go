package main

import (
	"log/slog"
	"os"

	"github.com/dutifuldev/ghreplica/internal/ghr"
)

func main() {
	if err := ghr.NewRootCmd().Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
