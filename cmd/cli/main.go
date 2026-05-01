package main

import (
	"fmt"
	"os"

	"github.com/kardianos/service"

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
	// Build the service descriptor first. If the binary is being run by
	// a service manager (no controlling TTY), service.Interactive()
	// returns false and we hand control to service.Run() which blocks
	// until the manager stops us. Otherwise we fall through to normal
	// CLI dispatch.
	svc, _, err := cli.BuildServiceForMain()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !service.Interactive() {
		if err := svc.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
