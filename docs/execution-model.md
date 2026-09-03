# Execution model

## Validation

`terragraph validate` (and `graph`/`plan`/`apply`/`destroy`, which run it first) reports two severities:
- **Error**: blocks the command (a `from`/`to` referencing a port that doesn't exist, two data edges targeting the same input after group expansion even when they are exact duplicates, a data edge and `vars` both setting the same input, a cycle, a value that doesn't fit the target variable's declared type, `backend_config` set on a module with no `backend` block, two nodes sharing a module directory with identical `backend_config` maps).
- **Warning**: printed but never blocks. A required variable with no edge feeding it may legitimately come from that module's own `terraform.tfvars` or the environment, outside the blueprint entirely.

Cycle detection reports every independent cyclic cluster in one pass (Tarjan's SCC algorithm), not just the first one found.

Type checking is a runtime check, not static inference: a standard root module's `output` blocks don't declare a type, so there's nothing to infer statically. Instead, by the time an edge is about to be wired the concrete value is already known: it's decoded directly against the target variable's declared type (the same mechanism Terraform itself uses to load `*.tfvars.json`), which is exact rather than a guess.

## How values are passed

terragraph never writes or modifies `.tf` files. Before running a node, it writes the values resolved from its incoming data edges (and its own `vars`, including literals a `use.vars` rewrote onto that node; see [blueprint.md](blueprint.md#literal-input-values-vars)) to an ephemeral, engine-managed tfvars file, then passes it to Terraform explicitly via `-var-file`. Terraform's own `*.auto.tfvars.json` auto-loading is never relied on: two nodes can share a module directory (see `backend_config` in [blueprint.md](blueprint.md#reusing-the-same-module-across-instances)), and auto-loading by a fixed filename would let them clobber each other's values.

Where that file is written is controlled by an optional `tfvars` block:

```hcl
tfvars {
  location = "workdir"   # default
}
```

- **`workdir`** (default): `<blueprint dir>/.terragraph/vars/<node>.tfvars.json`, next to the node's other engine-managed state (`tfdata/`, `plans/`, and for `backend "local"` modules that do not set `path`, `state/<node>.tfstate`). Never touches a module's own directory, so nothing needs adding to any module's `.gitignore`, and two nodes sharing a `source` never collide on a filename. This is the right choice for a vendored module (not yours to add a `.gitignore` entry to) or one reused across many near-identical instances. If a module already declared `backend "local"` and kept `terraform.tfstate` in the module directory, the next `plan`/`apply` points state at `.terragraph/state/<node>.tfstate` instead; migrate with `terraform init -migrate-state` per node if you need the old state. `destroy` still uses the backend last cached in `TF_DATA_DIR` (it does not re-run `init`).
- **`module`**: `<node source>/.terragraph.<node>.tfvars.json`, alongside the module's own `.tf` files, for a resolved input value visible next to its source while debugging. Add the pattern below to each module's `.gitignore`:

  ```
  .terragraph.*.tfvars.json
  ```

  `terragraph validate` warns (never deletes) about a stale file left behind in a shared module directory by a node that's since been renamed or removed from the blueprint.

## Execution levels and parallelism

Nodes are grouped into levels: every node in level *i* only depends on nodes in levels `< i`, so nodes within one level have no edge between them and are safe to run concurrently. `terragraph graph` prints these levels directly. Execution defaults to sequential (`--parallelism 1`); pass `--parallelism N` to `plan`/`apply`/`destroy` to run up to `N` nodes within a level concurrently. Output from concurrent nodes is buffered and flushed as one `=== node <name> ===` block per node so it never interleaves; with the default `--parallelism 1` it streams live as before.

## Concurrent CLI processes

`--parallelism` is in-process. Two separate `terragraph` invocations against the same blueprint are a different boundary: they would otherwise share `.terragraph/` (tfvars, `TF_DATA_DIR`, saved plans) and, for nodes that reuse a module `source`, that directory's `.terraform.lock.hcl`.

`plan`, `apply`, `destroy` and `vendor` take an exclusive lock at `<blueprint dir>/.terragraph/lock` before they read or write module files, and hold it until the command exits. A second process targeting the same blueprint prints a one-line wait notice and blocks until the first exits; the lock is released on process exit, so a crash cannot leave it stuck. `validate`, `graph` and `language-server` do not take it, so they stay usable while a long apply is running. One process that already holds the lock can still use `--parallelism` inside the run.

## Deciding whether a node needs applying

Terraform decides, every run. `terragraph apply` plans each node with `-refresh=true -detailed-exitcode`; a plan reporting no changes skips the apply, and nothing local is consulted first.

If the plan reports changes, **that same plan is what gets applied** — it is written with `-out` and handed to `apply` — so the node refreshes once rather than twice, and the change that gets made is provably the change that was planned. An argument injected into apply alone (`TF_CLI_ARGS_apply`) can no longer make apply do something the plan never described: Terraform ignores scope arguments when applying a saved plan and rejects `-var` outright. `-refresh=true` is always passed explicitly, and a command-line flag beats the same flag arriving through `TF_CLI_ARGS_plan`, so an ambient `-refresh=false` cannot turn the check into a stale-state one.

The plan file lives at `<blueprint dir>/.terragraph/plans/<node>.tfplan` and is removed when the run ends. Like the tfvars file, it holds resolved input values in cleartext, so the same "keep `.terragraph/` out of version control" rule applies.

A node on the `remote` or `cloud` backend cannot produce a local plan file: those backends run the plan on HCP rather than locally. `terragraph apply` refuses that node rather than applying without inspecting the plan. Every backend that runs operations locally — `s3`, `gcs`, `azurerm`, `http`, `local`, ... — can write a plan file and is unaffected. (`s3`/`gcs`/`azurerm`/`http` keep state remote; `local` keeps it on disk.) HCP support is left for later; until then, use one of those backends.

## What a node may do: `approve`

Walking the graph and committing changes are two different things. terragraph automates the first — it runs nodes in dependency order and feeds each one's real outputs into the next. The second is a decision, and in a graph it is one nobody can make up front: a downstream node's plan cannot exist until its upstream has actually been applied (see the known limitation below). So it is delegated in advance, per node, in terms of what the plan turns out to contain.

That matters here more than it does for a single `terraform apply`, because a change propagates: node A's output changes, node B's input changes with it, and node B may replace its resources. Nobody saw B's plan before the run started, and nobody could have.

Each node resolves to one of three levels, defined by which of Terraform's planned actions they permit:

| level | permits | |
| --- | --- | --- |
| `none` | — | planned, never applied |
| `safe` | `create`, `update` | **default** |
| `all` | `create`, `update`, `replace`, `delete` | |

`replace` is gated as tightly as `delete`, because destroy-and-recreate is what an upstream change most often causes downstream and is just as irreversible.

`safe` is a workable default rather than an obstacle. A first-ever bootstrap is all creates; ordinary steady-state work is updates; and **reconciling drift is a create too** — a resource that has vanished remotely is dropped from state by the refresh, so the plan rebuilds it rather than deleting anything. Only a genuinely destructive plan stops.

Say it on the node where a destructive plan is by design:

```hcl
node "db" {
  source  = "./stacks/db"
  approve = "all"   # recreation is normal here
}
```

...or for one run:

```
terragraph apply                    # approve = safe
terragraph apply --approve=all      # this run, on the operator's judgement
```

Resolution follows the same layering as [`runtime`](blueprint.md#choosing-a-runtime-per-node-runtime) and [`env`](blueprint.md#extra-environment-variables-per-node-env):

```
node's own approve  >  enclosing use block  >  --approve  >  safe
```

...including the rule that a CLI flag only ever fills a gap nothing else spoke to. `--approve=all` does not override a node that declared `approve = "safe"`; a node that declared `approve = "all"` is not reined in by `--approve=none`. The blueprint is where a standing decision lives, and it goes through review.

When a node's plan exceeds its level, the run stops **before that node is applied**. Levels execute in order, so nothing downstream runs either — the cascade is cut at its source rather than audited afterwards:

```
node vpc: 3 to add, 1 to change, 0 to destroy
node eks: 0 to add, 2 to change, 0 to destroy
node api: 0 to add, 1 to change, 2 to destroy
error: node "api" plans 2 change(s) its approve level (safe) does not permit:
  aws_instance.web[0]   delete
  aws_db_instance.main  replace

Stopped before applying, so no later level ran.
If this is intended, declare approve = "all" on that node, or re-run with --approve=all.
```

This is checked whether or not anyone is watching. An interactive `yes` answers "apply this plan", not "override the standing policy"; overriding it is `--approve=all`, which is a visible, deliberate act.

The check reads the saved plan file, so it cannot run on the `remote`/`cloud` backends, which have none. Apply stops on those nodes with an error rather than applying uninspected.

## Approval

`approve` decides what *may* happen unattended. This decides *whether someone is asked*; the two are independent.

Without `--auto-approve`, terragraph asks before applying each node that has changes:

```
Apply these changes to node eks? [y/N]:
```

It asks about the plan you were just shown, and applies exactly that plan — a downstream node's plan cannot be produced ahead of time (see the known limitation below), so approval necessarily happens node by node, as the run reaches each one.

- Only `y` or `yes` approves. Declining stops the run: every later level consumes this node's outputs, so continuing past it would be applying against values that were never produced.
- A node with **no** changes is never asked about, so `terragraph apply` with no flags remains a usable "is everything still applied?" check with no terminal attached.
- Input may be piped (`echo yes | terragraph apply`). If a node needs approving and there is nothing to read — a CI runner, `</dev/null` — the run stops and says to pass `--auto-approve`, rather than reporting a refusal nobody made.
- `--parallelism N` (N > 1) requires `--auto-approve`: output from concurrent nodes is buffered and flushed a node at a time, so there is nowhere to put a prompt.

`terragraph destroy` is confirmed the same way, except the question comes from Terraform itself: there is no saved plan for terragraph to ask about on its behalf.

### There is no local cache

Earlier versions kept a content-addressed cache at `<blueprint dir>/.terragraph/cache.json`, hashing each node's source files, resolved inputs, runtime and `env` to decide whether it could skip apply. Hashing local files is a proxy for a remote fact, and the gap between the two produced a series of bugs: a backend or inherited `env` change that the key never modelled, remote drift that no local hash could see, and files consumed through `file()`/`templatefile()` that never invalidated anything.

Once every cache *hit* had to be confirmed by a refreshed plan anyway, the only thing the cache still did was send every *miss* straight to apply without one — so a genuinely unchanged node was re-applied on any fresh checkout, any CI runner without a warm `.terragraph/`, and every run after a `destroy`. Removing it puts every node behind the plan, which is both correct and skips more.

`--force` existed to bypass that cache. It is accepted and ignored, and will be removed in a later release. `terragraph destroy` no longer has to invalidate anything either.

## Known limitation

Planning a node whose upstream has never been applied is inherently impossible: its output value doesn't exist yet, and each node has its own independent state (there's no shared unknown-value mechanism to borrow, the way a single Terraform run has for values within one plan). `terragraph plan` reports this clearly instead of guessing. `terragraph apply` handles the full bootstrap by applying in topological order and feeding real values forward as they're produced.
