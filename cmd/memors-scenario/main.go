// memors-scenario is the development tool for the living-system scenario
// files under internal/eval/testdata/scenarios: it draws them as SVG,
// validates them, runs them and serves the studio. It is not part of the
// memors-mcp release.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kKEo/memors/internal/eval/scenario/scenariocli"
	"github.com/kKEo/memors/internal/eval/scenario/studio"
)

func main() {
	scenariocli.Serve = studio.Command
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(scenariocli.Main(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
