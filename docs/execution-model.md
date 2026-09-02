# Execution model

## Validation

`terragraph validate` (and `graph`/`plan`/`apply`/`destroy`, which run it first) reports two severities:
- **Error**: blocks the command (a `from`/`to` referencing a port that doesn't exist, a cycle, a value that doesn't fit the target variable's declared type).
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

- **`workdir`** (default): `<blueprint dir>/.terragraph/vars/<node>.tfvars.json`, next to the node's other engine-managed state (`tfdata/`, `cache.json`). Never touches a module's own directory, so nothing needs adding to any module's `.gitignore`, and two nodes sharing a `source` never collide on a filename. This is the right choice for a vendored module (not yours to add a `.gitignore` entry to) or one reused across many near-identical instances.
- **`module`**: `<node source>/.terragraph.<node>.tfvars.json`, alongside the module's own `.tf` files, for a resolved input value visible next to its source while debugging. Add the pattern below to each module's `.gitignore`:

  ```
  .terragraph.*.tfvars.json
  ```

  `terragraph validate` warns (never deletes) about a stale file left behind in a shared module directory by a node that's since been renamed or removed from the blueprint.

## Execution levels and parallelism

Nodes are grouped into levels: every node in level *i* only depends on nodes in levels `< i`, so nodes within one level have no edge between them and are safe to run concurrently. `terragraph graph` prints these levels directly. Execution defaults to sequential (`--parallelism 1`); pass `--parallelism N` to `plan`/`apply`/`destroy` to run up to `N` nodes within a level concurrently. Output from concurrent nodes is buffered and flushed as one `=== node <name> ===` block per node so it never interleaves; with the default `--parallelism 1` it streams live as before.

## Incremental apply

`terragraph apply` skips a node (without touching Terraform at all beyond reading its existing outputs) when neither its source files nor its resolved input values have changed since its last recorded apply (a content-addressed cache, in the spirit of Bazel/Nix, stored at `<blueprint dir>/.terragraph/cache.json`). Pass `--force` to bypass it and always re-run. `terragraph destroy` drops the cache entries for whatever it actually tore down, so a later `apply` never mistakes now-gone infrastructure for "unchanged."

## Known limitation

Planning a node whose upstream has never been applied is inherently impossible: its output value doesn't exist yet, and each node has its own independent state (there's no shared unknown-value mechanism to borrow, the way a single Terraform run has for values within one plan). `terragraph plan` reports this clearly instead of guessing. `terragraph apply` handles the full bootstrap by applying in topological order and feeding real values forward as they're produced.
