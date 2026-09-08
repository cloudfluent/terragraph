// Command terragraph runs the graph-based Terraform/OpenTofu orchestration engine: it resolves output -> input wiring between independent root modules and executes them in dependency order.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/cloudfluent/terragraph/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...", set by .goreleaser.yml for released binaries; a `go install`/`go build` outside that pipeline keeps this default.
var version = "dev"

func main() {
	root := cli.NewRootCmd(version)
	command, _, _ := root.Find(os.Args[1:])
	name := ""
	if command != nil {
		name = command.Name()
	}
	ctx, stop := executionContext(name)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if name == "run" {
			var native interface{ ExitCode() int }
			if errors.As(err, &native) && native.ExitCode() > 0 {
				os.Exit(native.ExitCode())
			}
		}
		os.Exit(1)
	}
}
