# Execution model

## Validation

`terragraph validate` (and `graph`/`plan`/`apply`/`destroy`, which run it first) reports two severities:
- **Error**: blocks the command (a `from`/`to` referencing a port that doesn't exist, a cycle, a value that doesn't fit the target variable's declared type).
- **Warning**: printed but never blocks. A required variable with no edge feeding it may legitimately come from that module's own `terraform.tfvars` or the environment, outside the blueprint entirely.

Cycle detection reports every independent cyclic cluster in one pass (Tarjan's SCC algorithm), not just the first one found.

Type checking is a runtime check, not static inference: a standard root module's `output` blocks don't declare a type, so there's nothing to infer statically. Instead, by the time an edge is about to be wired the concrete value is already known: it's decoded directly against the target variable's declared type (the same mechanism Terraform itself uses to load `*.tfvars.json`), which is exact rather than a guess.

## How values are passed

terragraph never writes or modifies `.tf` files. Before running a node, it writes the values resolved from its incoming data edges (and its own `vars`, see [blueprint.md](blueprint.md#literal-input-values-vars)) to `<node source>/.terragraph.auto.tfvars.json`, an ephemeral file Terraform loads automatically. Add this pattern to each module's `.gitignore`:

```
.terragraph.auto.tfvars.json
```

## Execution levels and parallelism

Nodes are grouped into levels: every node in level *i* only depends on nodes in levels `< i`, so nodes within one level have no edge between them and are safe to run concurrently. `terragraph graph` prints these levels directly. Execution defaults to sequential (`--parallelism 1`); pass `--parallelism N` to `plan`/`apply`/`destroy` to run up to `N` nodes within a level concurrently. Output from concurrent nodes is buffered and flushed as one `=== node <name> ===` block per node so it never interleaves; with the default `--parallelism 1` it streams live as before.

## Incremental apply

`terragraph apply` skips a node (without touching Terraform at all beyond reading its existing outputs) when neither its source files nor its resolved input values have changed since its last recorded apply (a content-addressed cache, in the spirit of Bazel/Nix, stored at `<blueprint dir>/.terragraph/cache.json`). Pass `--force` to bypass it and always re-run. `terragraph destroy` drops the cache entries for whatever it actually tore down, so a later `apply` never mistakes now-gone infrastructure for "unchanged."

## Known limitation

Planning a node whose upstream has never been applied is inherently impossible: its output value doesn't exist yet, and each node has its own independent state (there's no shared unknown-value mechanism to borrow, the way a single Terraform run has for values within one plan). `terragraph plan` reports this clearly instead of guessing. `terragraph apply` handles the full bootstrap by applying in topological order and feeding real values forward as they're produced.
