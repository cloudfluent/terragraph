# Execution model

## Validation

`terragraph validate` (and `graph`/`plan`/`apply`/`destroy`, which run it first) reports two severities:
- **Error**: blocks the command (a `from`/`to` referencing a port that doesn't exist, two data edges targeting the same input after group expansion even when they are exact duplicates, a data edge and `vars` both setting the same input, a cycle, a value that doesn't fit the target variable's declared type).
- **Warning**: printed but never blocks. A required variable with no edge feeding it may legitimately come from that module's own `terraform.tfvars` or the environment, outside the blueprint entirely.

Cycle detection reports every independent cyclic cluster in one pass (Tarjan's SCC algorithm), not just the first one found.

Type checking is a runtime check, not static inference: a standard root module's `output` blocks don't declare a type, so there's nothing to infer statically. Instead, by the time an edge is about to be wired the concrete value is already known: it's decoded directly against the target variable's declared type (the same mechanism Terraform itself uses to load `*.tfvars.json`), which is exact rather than a guess.

## How values are passed

terragraph never writes or modifies `.tf` files. Before running a node, it writes the values resolved from its incoming data edges (and its own `vars`, see [blueprint.md](blueprint.md#literal-input-values-vars)) to an ephemeral, engine-managed tfvars file, then passes it to Terraform explicitly via `-var-file`. Terraform's own `*.auto.tfvars.json` auto-loading is never relied on: two nodes can share a module directory (see `backend_config` in [blueprint.md](blueprint.md#reusing-the-same-module-across-instances)), and auto-loading by a fixed filename would let them clobber each other's values.

Where that file is written is controlled by an optional `tfvars` block:

```hcl
tfvars {
  location = "workdir"   # default
}
```

- **`workdir`** (default): `<blueprint dir>/.terragraph/vars/<node>.tfvars.json`, next to the node's other engine-managed state (`tfdata/`, `plans/`). Never touches a module's own directory, so nothing needs adding to any module's `.gitignore`, and two nodes sharing a `source` never collide on a filename. This is the right choice for a vendored module (not yours to add a `.gitignore` entry to) or one reused across many near-identical instances.
- **`module`**: `<node source>/.terragraph.<node>.tfvars.json`, alongside the module's own `.tf` files, for a resolved input value visible next to its source while debugging. Add the pattern below to each module's `.gitignore`:

  ```
  .terragraph.*.tfvars.json
  ```

  `terragraph validate` warns (never deletes) about a stale file left behind in a shared module directory by a node that's since been renamed or removed from the blueprint.

## Execution levels and parallelism

Nodes are grouped into levels: every node in level *i* only depends on nodes in levels `< i`, so nodes within one level have no edge between them and are safe to run concurrently. `terragraph graph` prints these levels directly. Execution defaults to sequential (`--parallelism 1`); pass `--parallelism N` to `plan`/`apply`/`destroy` to run up to `N` nodes within a level concurrently. Output from concurrent nodes is buffered and flushed as one `=== node <name> ===` block per node so it never interleaves; with the default `--parallelism 1` it streams live as before.

## Deciding whether a node needs applying

Terraform decides, every run. `terragraph apply` plans each node with `-refresh=true -detailed-exitcode`; a plan reporting no changes skips the apply, and nothing local is consulted first.

If the plan reports changes, **that same plan is what gets applied** — it is written with `-out` and handed to `apply` — so the node refreshes once rather than twice, and the change that gets made is provably the change that was planned. An argument injected into apply alone (`TF_CLI_ARGS_apply`) can no longer make apply do something the plan never described: Terraform ignores scope arguments when applying a saved plan and rejects `-var` outright. `-refresh=true` is always passed explicitly, and a command-line flag beats the same flag arriving through `TF_CLI_ARGS_plan`, so an ambient `-refresh=false` cannot turn the check into a stale-state one.

The plan file lives at `<blueprint dir>/.terragraph/plans/<node>.tfplan` and is removed when the run ends. Like the tfvars file, it holds resolved input values in cleartext, so the same "keep `.terragraph/` out of version control" rule applies.

A node on the `remote` or `cloud` backend falls back to planning and applying as two separate invocations: these run the plan on HCP rather than locally and cannot produce a local plan file. Every state-storage backend — `s3`, `gcs`, `azurerm`, `http`, `local`, ... — keeps state remote but runs operations locally, and is unaffected.

## Approval

Without `--auto-approve`, terragraph asks before applying each node that has changes:

```
Apply these changes to node eks? [y/N]:
```

It asks about the plan you were just shown, and applies exactly that plan — a downstream node's plan cannot be produced ahead of time (see the known limitation below), so approval necessarily happens node by node, as the run reaches each one.

- Only `y` or `yes` approves. Declining stops the run: every later level consumes this node's outputs, so continuing past it would be applying against values that were never produced.
- A node with **no** changes is never asked about, so `terragraph apply` with no flags remains a usable "is everything still applied?" check with no terminal attached.
- Input may be piped (`echo yes | terragraph apply`). If a node needs approving and there is nothing to read — a CI runner, `</dev/null` — the run stops and says to pass `--auto-approve`, rather than reporting a refusal nobody made.
- `--parallelism N` (N > 1) requires `--auto-approve`: output from concurrent nodes is buffered and flushed a node at a time, so there is nowhere to put a prompt.

### There is no local cache

Earlier versions kept a content-addressed cache at `<blueprint dir>/.terragraph/cache.json`, hashing each node's source files, resolved inputs, runtime and `env` to decide whether it could skip apply. Hashing local files is a proxy for a remote fact, and the gap between the two produced a series of bugs: a backend or inherited `env` change that the key never modelled, remote drift that no local hash could see, and files consumed through `file()`/`templatefile()` that never invalidated anything.

Once every cache *hit* had to be confirmed by a refreshed plan anyway, the only thing the cache still did was send every *miss* straight to apply without one — so a genuinely unchanged node was re-applied on any fresh checkout, any CI runner without a warm `.terragraph/`, and every run after a `destroy`. Removing it puts every node behind the plan, which is both correct and skips more.

`--force` existed to bypass that cache. It is accepted and ignored, and will be removed in a later release. `terragraph destroy` no longer has to invalidate anything either.

## Known limitation

Planning a node whose upstream has never been applied is inherently impossible: its output value doesn't exist yet, and each node has its own independent state (there's no shared unknown-value mechanism to borrow, the way a single Terraform run has for values within one plan). `terragraph plan` reports this clearly instead of guessing. `terragraph apply` handles the full bootstrap by applying in topological order and feeding real values forward as they're produced.
