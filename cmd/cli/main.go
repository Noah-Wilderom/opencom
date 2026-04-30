package main

import (
	"os"

	"opencom/internal/cli"
)

// Build info is injected directly into internal/cli via -ldflags by goreleaser:
//
//	-X opencom/internal/cli.Version=...
//	-X opencom/internal/cli.Commit=...
//	-X opencom/internal/cli.BuildDate=...
//
// We deliberately do NOT mirror them in this package: doing so creates an
// ordering bug where the natural ldflags target gets clobbered at startup.
func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
