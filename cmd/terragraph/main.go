// Command terragraph runs the graph-based Terraform/OpenTofu orchestration engine: it resolves output -> input wiring between independent root modules and executes them in dependency order.
package main

import (
	"fmt"
	"os"

	"github.com/cloudfluent/terragraph/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...", set by .goreleaser.yml for released binaries; a `go install`/`go build` outside that pipeline keeps this default.
var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
