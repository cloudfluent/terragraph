# TerraGraph

[![CI](https://github.com/cloudfluent/terragraph/actions/workflows/ci.yml/badge.svg)](https://github.com/cloudfluent/terragraph/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/cloudfluent/terragraph.svg)](https://pkg.go.dev/github.com/cloudfluent/terragraph) [![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

Split infrastructure into independent Terraform modules and you lose the one thing a single workspace gives you for free: one module's outputs feeding straight into another's inputs. In practice that gap gets closed by hand: `apply`, copy a value, paste it into the next module's tfvars, repeat. Or it gets closed by giving up the isolation and merging everything back into one giant workspace.

terragraph closes that gap without either tradeoff. Every root module stays completely standalone (its own backend, its own providers, its own resources), and a separate file declares the **wiring**: which output feeds which input. terragraph reads that file, works out the dependency order, and passes the real values through automatically as it applies each module.

## Install

macOS via Homebrew:

```
brew install --cask cloudfluent/tap/terragraph
```

Prebuilt binaries for Linux, macOS, and Windows (amd64/arm64) are attached to each [release](https://github.com/cloudfluent/terragraph/releases). Or build from source:

```
go install github.com/cloudfluent/terragraph/cmd/terragraph@latest
```

Requires Go 1.27.1+ to build, and `terraform` or `tofu` on `PATH` to run.

## Quick look

A blueprint (`blueprint.hcl`) is a flat list of `node` and `edge` facts:

```hcl
node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }

edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
```

```
terragraph apply --parallelism 2 --auto-approve
```

`terragraph` resolves the graph, runs `terraform`/`tofu` for each node in dependency order, and passes `vpc`'s real `vpc_id` output into `eks`'s input at runtime, with no generated code and no shared state file. See [`examples/basic`](examples/basic) for this exact setup running end to end.

## Documentation

See [docs/](docs/README.md) for the blueprint model, groups, vendoring, the execution model, and the full CLI reference.

## Examples

Self-contained and cloud-credential-free (`random`/`local` providers only). Clone and run directly, each with its own README:

- [`examples/basic`](examples/basic): one node feeding two independent downstream nodes (wiring, parallel execution, incremental apply).
- [`examples/reuse`](examples/reuse): the same module instantiated twice via distinct `backend_config`, proving state isolation.
- [`examples/group`](examples/group): a group instantiated via `use`, proving expansion and export wiring.
- [`examples/vendored`](examples/vendored): a node sourced from a remote git address, showing the vendor workflow.

## Development

```
make check   # fmt-check + lint + docs-check + build + test, exactly what CI runs
make fmt     # reformat in place
make docs    # regenerate docs/cli/*.md from the live CLI
make build   # ./terragraph
make test    # go test ./... -race
```

`make help` lists every target. `make lint`/`fmt`/`fmt-check` fetch a pinned `golangci-lint` into `./bin/` (gitignored) on first use.

## License

[Apache License 2.0](LICENSE).
