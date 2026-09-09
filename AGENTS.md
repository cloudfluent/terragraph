# AGENTS.md

Conventions for AI agents and reviewers working on terragraph. Process rules (PR title, squash-merge, release-please, review requirements) live in [`CONTRIBUTING.md`](CONTRIBUTING.md) and are not repeated here.

terragraph is a Go CLI that orchestrates independent Terraform/OpenTofu root modules as a graph. It shells out to `terraform`/`tofu`; it never generates or edits `.tf` files.

# Verifying changes

`make check` is the gate. It runs exactly what CI runs, in the same order: `fmt-check`, `lint`, `docs-check`, `build`, `test`, `vscode-check`. Run it before claiming a change is done.

Narrower loops while iterating:

- `make test`: full suite with `-race` (the race detector is always on; don't propose disabling it)
- `go test ./internal/graph -run TestBuild_UseVars`: one package or one test
- `make fmt`: reformat in place
- `make docs`: regenerate `docs/cli/*.md`

Go version is pinned to the `go.mod` value and matched in CI. Don't bump it as a side effect of another change.

# Layout

`cmd/terragraph` is the entrypoint; implementation lives under `internal/`, one domain per package, with the public SDK under `plugin/` and first-party implementations under `plugins/`:

| Package | Owns |
|---|---|
| `blueprint` | Parsing `blueprint.hcl` / `group.hcl` into typed values |
| `graph` | Building and validating the node graph, cycle detection, group expansion |
| `plugins` (internal) | Executable plugin host: package installation, locks, sessions, and diagnostics |
| `privatefs` | Shared protected directory and file creation, including platform ACLs |
| `plugin` (public) | SDK descriptors, typed values, logging, and RPC protocol; no engine orchestration |
| `engine` | Execution: levels, plan/apply/destroy, input resolution, approval |
| `exec` | The terraform/tofu subprocess wrapper and the ephemeral tfvars file |
| `vendor` | Fetching and rewriting module sources |
| `cli` | Cobra commands, flag wiring, output formatting |
| `language`, `lsp` | Editor intelligence |
| `runlock` | Cross-process advisory lock on a blueprint directory |
| `module` | Terraform module introspection |
| `pathidentity` | Read-only filesystem path equality, including known directory case rules |

Dependencies flow one way: `cli` → `engine` → `graph` → `blueprint`, `cli` → `plugins` and `engine` → `plugins` → `blueprint`, and `lsp` → `language` → `blueprint`. The host also uses the leaf `exec` package for process containment and `privatefs` for protected work. `engine` shares `privatefs` for saved plans and journals. The host imports the public `plugin` SDK; the SDK never imports `internal/` packages. `blueprint`, `exec`, `module`, `runlock`, `privatefs`, and `pathidentity` are leaves: they import nothing else under `internal/`, and keeping them that way is what makes them testable in isolation. Don't add an import that reverses the direction or gives a leaf a dependency, and don't reach into another package to do work it should expose.

Before adding a package, function, or type, check whether an existing one already covers it. Extend the existing implementation rather than building a parallel one.

# Execution invariants

These are the ones that fail silently, so they are worth stating even though the code enforces them today.

- **Every node always gets its own isolated `TF_DATA_DIR`** via `exec.Runner.DataDir`, not just nodes that share a `source`. Terraform caches which backend it was last configured with inside `.terraform/`, keyed by working directory, so two nodes sharing one data dir break each other's backend configuration rather than failing loudly. Never make this conditional.
- **terragraph never generates or edits a `.tf` file, and never writes inside a module's own directory.** That is the invariant; the count of files is not. What it does write, all of it engine-managed and none of it authored by a user: the ephemeral tfvars (`internal/exec`), the vendor manifest and the vendored source trees it fetches under the root blueprint's vendor directory by qualified leaf name (including leaves inside local groups) (including `.terragraph-source.json` package-layout metadata for a source subdirectory) (`internal/vendor`), the advisory lock at `.terragraph/lock` (`internal/runlock`), the gitignored opt-in output snapshots at `.terragraph/outputs/<node>.json` (`internal/engine`, see docs/execution-model.md), the process-unique observation and recovery caches at `.terragraph/tfdata-read/read-*/<node>` and `.terragraph/tfdata-read/recovery-*` (engine-owned, protected like saved plans, removed before releasing the local lock; crash leftovers may contain backend credentials), the saved plan at `.terragraph/plans/<node>.tfplan` — whose path the engine owns and removes even though Terraform writes the bytes (`internal/engine`) — the backend-context fingerprints at `.terragraph/tfdata/<node>/.terragraph-context.json` (cache guards, never infrastructure state; process-unique `.prepare-context-*` files under `.terragraph/tfdata/` protect directory creation and are removed immediately), protected native backup scratch files at `.terragraph/backups/<run-id>.tfstate` (kept if archival fails), the protected execution objects under `.terragraph/executions/` or a dedicated S3 prefix (`internal/engine`, with optional retained plan bytes and native backups stored separately from journals), and, when the blueprint declares a `lock` block, a remote lock object (`internal/graphlock`). `.terragraph/state/<node>.tfstate` is *not* one of ours: it is a path filled into a module's own local backend, which Terraform then writes (`internal/graph`). A write path outside this list is a design change, not an implementation detail.
- **Plugin-owned write paths are explicit.** `terragraph.plugins.lock.json` records reviewed platform-specific package choices; `.plugins-lock-*` is temporary atomic publication scratch. `.terragraph/plugins/packages/<digest>/<platform>/` stores verified executable packages, sibling `.install-*` directories are installation scratch, and `.terragraph/plugins/work/evaluation-*/<alias>/` is private per-session scratch. Static function sessions close after graph construction, before Terraform execution. Runtime lifecycle sessions and execution credential leases close after runtime completion, before locks are released. `.terragraph/plugins/work/runtime-*/` owns private per-invocation work, optional protected `receipts.json`, and atomic `.receipt-*` scratch for commands without execution records; unresolved cleanup receipts remain for inspection. The RPC library owns temporary transport sockets under the OS temporary directory. Plugins never receive an API for writing module files.
- **`apply` and `destroy` with `--parallelism > 1` require `--auto-approve`.** Output from concurrent nodes is buffered, so there is nowhere to ask for approval. `plan` has no approval prompt and needs no such flag. Any new concurrent path must not be able to reach an interactive prompt.
- **`runlock` serializes processes, not nodes.** It is one advisory lock per blueprint directory, held for the whole run. In-process `--parallelism` runs under that single lock and is unaffected by it. Don't acquire it per node or conflate the two.
- **Execution records and optional retained plan objects belong under `.terragraph/executions/` or a dedicated configured S3 prefix.** The engine owns these protected objects; they never replace Terraform state. Store operations run under the existing blueprint and configured graph locks, and record updates and deletion must check the revision instead of overwriting another execution's decision. Local pending writes are not executable artifacts.
- **`validate`, `graph`, `plan`, `apply`, and `destroy` never import `internal/vendor`.** They only ever see the local directories vendoring already produced.

# Comments

Write **one dense line explaining why**, placed directly above the declaration, including on struct fields, where most of this repo's hard-won context lives. Do not wrap a rationale across several `//` lines when one line carries it; do not narrate what the code plainly does.

The bar is set by comments like the one on `exec.Runner.DataDir`, which explains the Terraform backend-cache collision that the field exists to prevent. Match that: name the failure mode the code is avoiding.

Package doc comments are required on every package and state what the package owns and what it deliberately does not do.

# Errors

- Wrap with `fmt.Errorf("...: %w", err)`. Preserve the chain; check with `errors.Is` / `errors.As`.
- Lowercase, no trailing punctuation.
- Prefix with the domain path the error is about, matching blueprint syntax: `node.%s.input.%s: ...`, `destroy: %w`, `init: %w`.
- **Include the remedy when there is one.** This repo's errors tell the user what to do: `"set by both a data edge and vars; remove one"`, `"--parallelism %d needs --auto-approve: output from concurrent nodes is buffered, so there is nowhere to ask for approval"`. An error that only reports a state is worse than one that also names the fix.
- No custom error types unless callers need to branch on them. `runlock.ErrHeld` is the pattern: a package-level sentinel with `errors.New`.

# Tests

The style here is **fixtures on disk, not mocks**:

- Build a real tree under `t.TempDir()` and write real `blueprint.hcl` / `.tf` files into it. Package-local helpers like `parseAndBuild` and `writeFixtureFile` exist for this; use them and mark helpers `t.Helper()`.
- Name tests `TestFunc_Scenario` (`TestBuild_UseVarsRewritesOntoLeaf`).
- Assert with `t.Fatalf("got = %v, want %q", got, want)`.
- Table-driven tests are the exception, not the default. Prefer one named test per behavior; reach for a table only when the cases are genuinely uniform.
- **No `t.Parallel()`.** Nothing in the suite uses it today; tests touch shared temp state and process-level locks.
- Assert on observable behavior, not internal structure. A test that would still pass with the bug reintroduced is not worth adding.

Platform-specific tests go in `_unix_test.go` with `//go:build !windows`, mirroring `_unix.go` for source.

# CLI output contract

- **stdout is the result.** Plain text by default, or the JSON payload under `--output json`.
- **stderr is diagnostics**, via `slog` at `--log-level` (default `warn`, so the CLI is silent unless something needs attention). Never write results to stderr or diagnostics to stdout.
- JSON payloads go through explicit `*DTO` structs in `internal/cli` with `json` tags. Internal types are never marshalled directly; `problemDTO` exists because `graph.Severity` would otherwise serialize as a bare int. Adding a field to a DTO changes a public contract; treat it as such.

# Cross-platform

terragraph targets Linux, macOS, and Windows; CI builds and tests all three.

- Always `filepath.Join` / `filepath.Separator`. Never hardcode `/` or `\`.
- Subprocesses go through `internal/exec`. Don't spawn `terraform`/`tofu` from anywhere else.
- File locking, signals, and process semantics differ on Windows, so put the divergence behind a build tag rather than a runtime `if`.

# Generated and vendored files

- `docs/cli/*.md` is generated from the cobra command tree by `tools/gendocs`. Never hand-edit it; change the command's help text and run `make docs`. CI fails on a stale diff.
- `CHANGELOG.md` and version numbers are owned by release-please. Never edit them in a normal PR.
- `editors/vscode` is a separate npm project with its own checks (`make vscode-check`).

# Documentation

If a change alters blueprint semantics, a CLI flag, or an example's expected output, update the matching `docs/*.md` or example `README.md` **in the same PR**. `docs/blueprint.md`, `docs/execution-model.md`, `docs/groups.md`, and `docs/vendoring.md` are the reference surface; they are meant to stay true, not to be caught up later.
