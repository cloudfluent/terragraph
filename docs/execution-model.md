# Execution model

terragraph runs independent root modules in dependency order. `plan` and `apply` visit upstream nodes first; `destroy` reverses that order. Each module keeps its own state.

## Inspecting the graph

Start with validation and a view of the execution order:

```sh
terragraph validate
terragraph graph
terragraph graph --format dot > graph.dot
```

The default graph output lists execution levels. DOT output can be rendered with Graphviz; solid edges carry data and dashed edges express ordering only. See [JSON run reports](#json-run-reports) for automation-friendly output.

## Selecting nodes

`graph`, `plan`, `apply`, and `destroy` select the whole graph by default. Repeat `--node <name>` to select exact leaves, including qualified names such as `checkout.cluster`. Add `--downstream` to include every reachable successor across both data edges and ordering-only edges. Multiple starting nodes contribute a union; each leaf runs once.

```sh
terragraph graph --node checkout.cluster --downstream
terragraph plan --node checkout.cluster --downstream
terragraph apply --node checkout.cluster --node payments.cluster --downstream
terragraph destroy --node checkout.cluster --downstream
```

A single `--node` without `--downstream` still selects only that leaf. Names are case-sensitive and are neither trimmed nor interpreted as patterns. Empty names, unknown names, group instance names, and `--downstream` without a starting node are errors. `--node b,x` is a literal name, not a comma-separated list; use `--node b --node x`. Positional node names such as `terragraph apply checkout.cluster` are rejected. `--downstream=false` does not expand a selection and preserves whole-graph behavior when no nodes were specified.

Membership is fixed before runtime calls. terragraph filters the original full-graph levels, removes empty levels, and numbers the remaining levels consecutively. It preserves alphabetical order within each level. It does not recompute a more parallel schedule after removing external dependencies. `destroy` uses the same membership with reversed levels. An unchanged upstream never skips a selected downstream node's fresh plan.

Selection limits execution, not validation or coordination: errors anywhere in the blueprint can block the run, and existing local/remote locks and unresolved-execution recovery barriers still apply. Per-node data directories, approval policies, failure handling, and the requirement for `--auto-approve` with concurrent apply/destroy are unchanged. A failure can follow successful changes to other selected nodes; selection provides no atomicity or automatic rollback.

### Dependencies outside the selection

Unselected nodes never receive plan/apply/destroy calls. A selected consumer may read an unselected producer's existing output using the usual input-resolution path, including snapshot fallback only when already opted in and eligible. Runtime compatibility checks may also inspect selected nodes and their direct data producers. Missing outputs fail with the existing remedy; they do not expand selection. Saved planning and saved application retain their stricter requirement for live upstream outputs.

Boundary edges preserve their original direction. Incoming data edges explain existing values a run may need; incoming ordering-only edges do not execute or verify completion of the external predecessor. Outgoing edges identify consumers that remain unselected and will not be updated by this invocation.

### Selection output and compatibility

An explicit `--node` or true `--downstream` adds a scope summary. Text execution commands print it to stdout before the first runtime subprocess, without introducing an approval step. Text graph output shows the summary and forward execution levels; destroy shows reverse execution levels. Early validation or selection errors may have no summary.

`graph` reads configuration only: it does not invoke Terraform/OpenTofu, write execution records, or verify output existence, accessibility, or freshness. A later ordinary command recomputes its selection from the then-current blueprint. Use a [saved execution](executions.md) to retain membership across commands.

JSON remains one result object, with an optional `selection` object alongside the existing `levels` or `nodes`. Plan retains its existing `schema_version` and `diagnostics`. A resolved selection is included even in preparation or execution failures; unresolved selection is omitted. Unselected leaves are never added to `nodes` as unchanged or not run. The selection contains names and relationships only, never output values, variables, or backend credentials.

For `a -> b -> c`, selecting `b` with downstream expansion produces:

```json
{
  "levels": [["b"], ["c"]],
  "selection": {
    "schema_version": 1,
    "mode": "downstream",
    "requested": ["b"],
    "nodes": [
      {"node": "b", "reason": "requested", "via": []},
      {"node": "c", "reason": "downstream", "via": ["b"]}
    ],
    "boundary_edges": [
      {"kind": "data", "from": {"node": "a", "output": "out"}, "to": {"node": "b", "input": "in"}}
    ]
  }
}
```

`mode` is `exact` or `downstream`. Requested leaves retain `reason: "requested"` even if also reachable from another seed. Other leaves list their selected immediate predecessors in `via`, not every possible path. `requested`, `nodes`, and `via` are sorted by name. Boundary edges sort by source node/port, destination node/port, then kind. Ordering edges use `kind: "ordering"` and omit ports. All array fields are arrays, including empty arrays. Execution order comes from `levels` or each result node's `level`, not selection array order.

Selected DOT output includes selected leaves and adjacent boundary context. External leaves are gray and labeled `not selected`; edges between two external leaves are omitted. Solid/dashed lines still distinguish data/ordering, and the legend and boundary labels distinguish scope. `--output json --format dot` remains invalid.

Without explicit selection, existing output stays unchanged. Single-node membership also stays unchanged, but it now has a text summary and optional JSON selection. Repeated `--node` flags now select a union, replacing the previous implementation's last-value-wins behavior. `output`, `status`, `vendor`, and `run` retain their existing single-node interfaces. This feature does not add group selection, wildcards, upstream expansion, exclusions, or automatic change detection.

## Validation

`terragraph validate` checks the blueprint and local module declarations without running Terraform/OpenTofu. `graph`, `plan`, `apply`, and `destroy` perform the same validation first:

- **Errors** block the command: missing ports, conflicting input sources after group expansion, cycles, or invalid backend configuration. All independent cycles are reported in one pass.
- **Warnings** allow execution: for example, a required input may come from the module's own tfvars or environment, and state may remain after a node is renamed. [Contract checks](contracts.md) also warn by default; `contracts { mode = "enforce" }` makes contract violations block the command.

Concrete values from `vars` and data edges are checked against the destination module's variable type when resolving inputs for `plan`, `apply`, or `destroy`. This accepts compatible conversions, optional object attributes and their defaults, and additional object attributes. The original value is still passed unchanged to Terraform/OpenTofu, which performs the final conversion and enforces variable defaults, nullability, and validation blocks. Static validation does not prove that real infrastructure or output values satisfy those declarations.

## Known limitation

A whole-graph `plan` uses **existing upstream outputs**, not values proposed by upstream plans. If an upstream change would alter an output, a downstream plan still receives the old value. It may therefore differ from the plan shown later during `apply`.

On a fresh graph, a consumer cannot be planned if a data edge needs an upstream output that is not yet available. Ordering-only edges do not have this restriction. `terragraph apply` handles bootstrap by applying upstream nodes first and passing their real outputs forward in the same run.

`terragraph apply` also currently rejects nodes using the `remote` backend or a `cloud` block, because its inspected saved-plan workflow does not support them. Both remote operations and [HCP's local execution mode](https://developer.hashicorp.com/terraform/language/backend/remote) are outside terragraph's current apply support. Use a supported backend such as `s3`, `gcs`, `azurerm`, `http`, or `local`.

## Deciding whether a node needs applying

Every `terragraph apply` starts each selected node with a fresh plan using `-refresh=true -detailed-exitcode`. A node with no changes skips apply. A changed node's plan is saved, inspected against its approval policy, shown for confirmation when required, and then applied **from that same saved plan**. A downstream node is planned when execution reaches it, using outputs available at that point.

The apply or review inspection saved plan lives at `<blueprint dir>/.terragraph/plans/<node>.tfplan` and is removed when the node finishes, including on failure. It contains input values in cleartext. Keep `.terragraph/` out of version control.

Saved plans are protected with directory mode `0700` and file mode `0600` on Unix, and a protected current-user DACL on Windows. Unsafe parent permissions or ownership are refused; existing plan-directory permissions are tightened where safe. Plan symlinks, and symlinks or Windows reparse points at `.terragraph` or `plans/`, are also refused. Use a private checkout or correct the permissions named in the error; on macOS, an extended ACL may require the suggested `chmod -N` remedy. These checks protect against access by other ordinary users, not administrators or processes running as you.

`--force` is deprecated and has no effect; every apply already checks current state with a refreshed plan.

## What a node may do: `approve`

The `approve` policy limits resource actions before a changed node is applied. It applies to both interactive runs and runs using `--auto-approve`.

| Policy | Permitted resource changes |
| --- | --- |
| `none` | No create, update, replace, or delete |
| `safe` | Create and update; the default |
| `all` | Create, update, replace, and delete |

Replacement is treated like deletion. `safe` describes permitted action categories; an in-place update can still affect a running service.

**`none` is not a preview mode.** Plans containing only output changes or reads can still be applied and write state. Use `terragraph plan` when you want a preview without apply.

Declare a standing policy on a node or [group instance](groups.md):

```hcl
node "db" {
  source  = "./stacks/db"
  approve = "all"
}
```

For nodes without a declared policy, choose one for the run:

```sh
terragraph apply --approve all
```

Resolution is `node's own approve > enclosing use > --approve > safe`. Nested groups use the nearest declaration. The CLI flag fills an unset policy: `--approve all` cannot override an explicit `approve = "safe"`, and `--approve none` cannot restrict an explicit `approve = "all"`.

When a plan exceeds its policy, that node fails before apply and later execution levels do not run. The error identifies the disallowed actions and the declaration or flag to change. Other nodes in the same level can still finish; see [failure and retry](#failure-and-retry).

`destroy` checks the selected nodes before running any of them. An explicit `approve = "none"` or `"safe"`, including one inherited from `use`, blocks teardown. Nodes without a declared policy are allowed to reach Terraform's own confirmation prompt. `destroy` has no `--approve` flag: change the declaration if teardown is intended. `--auto-approve` does not bypass this policy.

## Approval

The `approve` policy controls allowed actions; `--auto-approve` controls confirmation questions.

Without `--auto-approve`, terragraph asks before applying each node that has changes and passes its policy:

```text
Apply these changes to node eks? [y/N]:
```

- Only `y` or `yes` approves. A refusal fails that node and prevents later levels from running.
- Unchanged nodes need no confirmation.
- Piped input is supported, but each changed node needs an answer. If no input is available, the command fails with a remedy to use `--auto-approve`.
- Both `--parallelism N` with N greater than 1 and `--output json` require `--auto-approve` for `apply` and `destroy`. `plan` needs no confirmation.

For `destroy`, the confirmation comes from Terraform/OpenTofu itself. It does not use a terragraph saved plan and does not rerun `init`; it uses the backend configuration already cached in the node's `TF_DATA_DIR`.

## Execution levels and parallelism

Nodes in the same execution level have no dependency edge between them. The default `--parallelism 1` runs them sequentially and streams output live. `--parallelism N` runs up to N nodes in a level concurrently, buffering output into a separate `=== node <name> ===` block per node. terragraph completes a level before starting the next one.

```sh
terragraph apply --parallelism 4 --auto-approve
```

This limit controls concurrent **nodes**; each Terraform/OpenTofu process still manages concurrency within its own module.

## Failure and retry

An ordinary node failure, policy rejection, or declined confirmation does not cancel its siblings: **the other nodes in that level continue, even with `--parallelism 1`**. No later level starts. Interrupting the command has different behavior, described [below](#interrupting-an-execution).

terragraph does not roll back completed changes. A failed apply may also have changed some resources before failing, or may have succeeded before a subsequent output read failed. Inspect the reported error and current state, fix the cause, and rerun `terragraph apply`. It plans again against current state and skips unchanged nodes. Use `--node` alone to retry that leaf; add `--downstream` when its reachable consumers should also be selected.

## How values are passed

terragraph never generates or edits `.tf` files. It resolves incoming data edges and [literal `vars`](blueprint.md#literal-input-values-vars), writes an ephemeral JSON tfvars file, and passes it explicitly with `-var-file`. Each node also has an isolated `TF_DATA_DIR`, including nodes that share the same source directory, so backend initialization is separate for every node.

A node resolving multiple inputs from the same upstream reuses one successful live output read for those inputs, so they cannot mix different state revisions. A subsequent input resolution reads live outputs again.

The optional `tfvars` block chooses where resolved values live while the node runs:

```hcl
tfvars {
  location = "workdir" # default
}
```

| Location | Temporary file |
| --- | --- |
| `workdir` (default) | `<blueprint dir>/.terragraph/vars/<node>.tfvars.json` |
| `module` | `<node source>/.terragraph.<node>.tfvars.json` |

The default avoids writing tfvars into module directories and works well for shared or vendored sources. With `module`, add `.terragraph.*.tfvars.json` to each module's `.gitignore`. Both locations contain cleartext inputs and are removed when the node finishes, including on failure. Unix files use mode `0600`; Windows tfvars use inherited filesystem permissions, without an explicit owner-only ACL. A crash or forced termination can leave files behind.

terragraph's input encoding and type-checking errors omit value-derived details, including object and map keys, when the destination variable or inspected upstream output is declared sensitive, or when the runtime output is sensitive or has missing or null sensitivity metadata. Runtime sensitivity is preserved both for live reads and for outputs produced earlier in the same run. Errors still identify the node, input, and expected type so you can check the value against the module's variable declaration. This also applies to JSON reports, but does not redact arbitrary Terraform/OpenTofu subprocess output.

For a module declaring `backend "local"`, terragraph supplies an absolute state path at `<blueprint dir>/.terragraph/state/<node>.tfstate` unless the node or an enclosing `use` sets `backend_config.path`. A path written only in the module's backend block is overridden. Terraform writes the state there. When adopting terragraph for an existing local state, plan the backend migration per node before applying; pointing at a new empty state can propose recreating existing resources. `destroy` uses the last initialized backend, as described [above](#approval).

`validate` warns about orphaned managed local state and stale module-location tfvars after a node is renamed or removed. It never deletes those files. Recover or migrate the state before removing it.

## Concurrent CLI processes

`plan`, `apply`, `destroy`, and `vendor` hold one exclusive local lock at `<blueprint dir>/.terragraph/lock` for the run. A second process on the same blueprint prints a wait notice and waits for the first to exit. The operating system releases this lock on exit, including a crash.

The lock coordinates processes in the same checkout; it does not limit `--parallelism` within a run or coordinate different machines. `validate`, `graph`, and `language-server` do not acquire it.

## Graph remote lock

An optional [top-level `lock` block](blueprint.md#graph-remote-lock-lock) serializes graph execution across machines. Per-node Terraform state locks still apply, but cannot by themselves protect the order of an entire graph run.

The graph lock is an S3 object created with conditional writes, not S3 Object Lock (WORM). Use a distinct object from every node's state and grant `s3:GetObject`, `s3:PutObject`, and `s3:DeleteObject` on the lock key. Every node must use a remote backend; validation rejects local or missing backends. The `remote`/`cloud` apply limitation still applies.

`plan`, `apply`, and `destroy` acquire the local lock, then the remote lock, then execute nodes. An existing remote lock **fails immediately**, rather than waiting. `validate`, `graph`, and `language-server` do not acquire it; `vendor` uses only the local lock.

A normal exit releases the object. Crashes, forced termination, or a failed S3 deletion can leave it behind. A release error is printed to stderr but does not turn an otherwise successful run into a failure.

To inspect a configured lock, run `terragraph force-unlock` without `--yes`; it refuses deletion and reports the object and, when readable, its holder. After verifying that the holder is no longer running, delete it with:

```sh
terragraph force-unlock --yes
```

Deletion is unconditional. The command refuses while another terragraph process holds the same checkout's local lock, but cannot detect a legitimate holder on another machine. It only parses the blueprint, so recovery also works before vendoring.

## Interrupting an execution

On Linux and macOS, Ctrl-C or SIGTERM during `plan`, `apply`, or `destroy` cancels local-lock waits and stops queued and downstream nodes. Active runtime process groups receive an interrupt, then SIGKILL after a five-second grace period. terragraph waits for them to stop before removing managed tfvars and saved plans and releasing locks. JSON reports retain selected nodes, including those `not run`.

Cleanup cannot run if terragraph itself crashes or receives SIGKILL. Descendants that detach into another process group are outside this guarantee; failed process inspection or an uninterruptible kernel wait can delay cleanup with locks held. Windows retains native console behavior and is outside this cancellation guarantee. It also does not cover `vendor` or `language-server`.

## JSON run reports

Use `--output json` for automation. Stdout carries the result; diagnostics, execution progress, and Terraform/OpenTofu output go to stderr.

```sh
terragraph validate --output json
terragraph graph --output json
terragraph vendor --output json
terragraph plan --output json
terragraph apply --output json --auto-approve
terragraph destroy --output json --auto-approve
```

| Command | JSON result |
| --- | --- |
| `validate` | Object with `valid` and `problems`; each problem has `code`, `category`, `phase`, `subject`, `severity`, `message`, and an optional `remedy`. Warnings can coexist with `valid: true`. |
| `graph` | Object with `schema_version: 1`, `diagnostics`, and `levels`, an array of arrays of node names. Cannot combine JSON with `--format dot`. |
| `vendor` | Array of results with `node`, `status` (`vendored`, `skipped`, or `error`), node `diagnostics`, and an optional `error`. Global failures use the shapes in [agent usage](agent-usage.md). |
| `plan`, `apply`, `destroy` | Object with `schema_version: 1`, `nodes`, `diagnostics`, and `execution_id` when an execution session was acquired; node entries retain `node`, `level`, `status`, and optional `error`, and add `diagnostics`. |

Run statuses are `planned`, `applied`, `unchanged`, `destroyed`, `failed`, or `not run`. Entries are ordered by execution level, then node name; destroy numbers levels in reverse dependency order.

Failures emit a single JSON result and exit nonzero when `--output json` can be identified and stdout is writable. Existing result shapes retain partial results; failures without a result use `{ "schema_version": 1, "diagnostics": [...] }`. Flag-parser failures use that error-only shape. Invalid or ambiguous output selection, help, native `run`, and output-write failures can still leave no JSON result. See [agent usage](agent-usage.md) for branching rules and compatibility details. JSON `apply` and `destroy` require `--auto-approve` even when sequential; the `approve` policy still applies.

## Output snapshots

Add `snapshots {}` to opt in to local output snapshots. They can help resolve downstream inputs when an upstream output read fails, for example during teardown. Without the block, terragraph neither writes nor reads snapshots.

Input resolution prefers this run's applied outputs, then live `terraform/tofu output`, then a snapshot **only if the live read failed**. A successful live read that lacks a particular output does not trigger fallback. A missing, corrupt, or incompatible snapshot leaves the live-read error intact.

Snapshots can be stale: they do not prove that upstream infrastructure still exists. Values useful for tearing down a consumer may point to nonexistent resources during apply. Restore live outputs when possible.

An opted-in apply writes `.terragraph/outputs/<node>.json`, including for unchanged nodes, with only outputs consumed by data edges. A value is stored only when both the current module schema marks it non-sensitive and the runtime output explicitly reports `sensitive: false`. Missing or null sensitivity metadata is treated as unknown. Sensitive or unknown outputs contribute only their names to a `withheld` list. Wrappers must preserve runtime sensitivity metadata.

Reads check the current module schema again. A now-sensitive output cannot come from a snapshot; live and same-run outputs can still carry sensitive values normally. If a needed value is withheld, restore live upstream outputs or apply the upstream in the same run. Changing an output back to non-sensitive also requires reapplying its producer before a snapshot can supply it.

Reapplying a producer rewrites its snapshot, removing values that became sensitive; if it has no consumed outputs, its old snapshot is removed. Opting out does not delete existing files, and old values remain on disk until rewritten or manually removed. Keep `.terragraph/` out of version control. Snapshot files use mode `0600` on Unix; Windows uses inherited filesystem permissions without an explicit owner-only ACL. Snapshots are local to each machine.

New snapshots use schema version 3 and preserve runtime output types. Version 2 remains readable but cannot establish collection kind for explicit contracts. Version 1 values did not verify runtime sensitivity and are treated as withheld. Restore live outputs or run `terragraph apply --node <producer>` to republish under the normal approval policy; a no-change apply also upgrades the snapshot. Reading an old file does not rewrite it. Older terragraph versions cannot consume schema 3.

Module inspection and compatibility checks follow the producer's [resolved runtime](blueprint.md#choosing-a-runtime-per-node-runtime), including upstream reads in a node-scoped run. Runtime-specific sensitivity declarations therefore apply to snapshot reads too; offline reads cannot refresh runtime output metadata.


## Observing outputs

`terragraph output` reads current outputs for every expanded leaf, in name order.
Use `output --node checkout.cluster`, or select one value with
`output --node checkout.cluster cluster_id`. A group prefix suggests leaf names;
a positional output name requires `--node` and is never parsed as a dotted address.

`--raw` prints one string, exact JSON number, or boolean without a newline.
It rejects collections, null, and `--output json`. Values marked sensitive by
the current module **or** the runtime are withheld by default, as are values
whose runtime sensitivity is unknown. Text prints `(sensitive)`; JSON retains
`sensitive` (including null for unknown) and `redacted`, but omits the value.
Only `--show-sensitive` discloses these values; raw requests otherwise fail.

Observation uses the existing exclusive local blueprint lock, including during
module inspection. It waits for vendor/apply/other local observations and can be
cancelled while waiting. It acquires no remote graph lock. The lock coordinates
Terragraph processes on this blueprint, not editors, other checkouts, or native
Terraform writers. Several node reads are not an atomic graph snapshot, and
successful reads do not prove that no writer is active.

Each session owns `.terragraph/tfdata-read/read-*/<node>`, separate from execution
caches. The parent uses the saved-plan permission and symlink/reparse-point
protections (0700 on Unix and a protected current-user DACL on Windows).
Private session descendants inherit this boundary. Normal completion, failures,
and supported cancellation remove the session before releasing the local lock.
A crash or forced termination can leave backend credentials there: keep
`.terragraph/` gitignored and remove abandoned read directories in a private
checkout when no observation is active. Sources and execution caches are unchanged.

Supported preparation is deliberately bounded:

| Runtime evidence | Backend | Workspace | Preparation |
| --- | --- | --- | --- |
| Terraform 1.5.7, OpenTofu 1.11.0 | explicit or implicit local | default | Existing regular state file required; no replacement is created. |
| Terraform 1.5.7, OpenTofu 1.11.0 | HTTP | default | Existing endpoint; real runtime tests verify GET-only requests, including HTTP 404 and authentication failure. |
| Other backends or named workspaces | any | any | Refused with a capability diagnostic before init. |

Other runtime versions using these backends must retain the same CLI behavior;
the versions above are the tested evidence, not a blanket certification of future
versions or custom wrappers. Both paths require the module's committed
`.terraform.lock.hcl` (an empty committed file suffices with no external providers).
Preparation uses `init -input=false -lockfile=readonly -reconfigure` in a fresh cache.
It never selects/creates a workspace or requests migration. Inherited and node
`TF_CLI_ARGS*` and `TF_LOG*` settings are cleared for observation so they cannot
inject migration flags or write secret-bearing logs. Credentials remain available
through ordinary backend configuration and environment variables.
The lockfile flag protects dependency selections; the backend restriction and
behavioral tests establish the narrower preparation guarantee.

Runtime observation streams are withheld because backend diagnostics can echo
credentials and values before sensitivity is known. Terragraph emits safe
diagnostics on stderr for text, and in the result for JSON. Module inspection
failures preserve other identifiable leaves. Broken blueprint/group syntax or
unresolvable group identity can still prevent the entire load. Execution-only
wiring, group exports, required inputs, and policy validation do not block reads.
There is no automatic output-snapshot fallback.

The JSON observation contract has `schema_version: 1`, deterministic `nodes`,
and `diagnostics`. Node status is `observed` or `failed`; diagnostics have
`code`, `category`, `severity`, `phase`, `subject`, `message`, and an optional `remedy`.
Unknown additive fields must be ignored; changing existing meanings requires
a new schema version. Missing named outputs, unavailable state, credential,
lockfile, preparation, and capability errors exit nonzero while retaining
successful siblings. Successfully observed zero outputs is normal.
Pre-execution load and selection failures produce the same envelope when JSON
was selected and stdout is writable. Identifiable JSON flag-parser failures use
the common error-only shape before command dispatch.


### Observing state status

`terragraph status [--node checkout.cluster] [--output json]` shares the output
session, backend matrix, source lock, and partial-failure contract. It returns
runtime, backend type, an opaque SHA-256 state identity (never the backend
configuration or credential-bearing endpoint), and safe resource-instance and
root-output counts. State values and output payloads are never returned.

`state` distinguishes `present`, `empty`, `absent`, `indeterminate`, and
`unavailable`. The runtime's version-4 state serialization is required.
An empty document at serial zero is indeterminate: real HTTP backends can
synthesize this response for a missing remote object. A nonempty document or
an empty document with a positive serial supplies observable evidence; a
successful empty stdout supplies absence evidence. A missing local state file
is always unavailable, and backend/credential errors never mean “not applied”.
These observations cannot prove a past apply succeeded or that there is no drift.
The identity is a comparison token, not an authorization or freshness proof.

## Structured plan review

Both text and `plan --output json` inspect a protected ephemeral saved plan and
derive their action summaries from the same normalized result. Existing
`nodes[].node`, `level`, `status`, and `error` fields retain their meanings;
a successful unchanged plan still has status `planned`. The additive JSON
envelope now declares `schema_version: 1` and top-level `diagnostics`.
Consumers must ignore unknown additive fields; incompatible meanings require
a schema-version change. Apply/destroy envelopes are unchanged.

Each node's `review` contains:

- `evidence_available` and nullable `has_changes`. Missing evidence is null,
  never an unchanged verdict.
- Resource addresses and raw actions, a distinct replacement category, and
  create/update/delete/replace/read/no-op/other counts. A replacement counts once
  in `replace`, rather than also inflating create and delete.
- Output names and actions separately. Output-only changes set `has_changes`;
  before/after resource or output values are not included.
- Effective `approve` and `policy_decision` (`pass`, `block`, or `unknown`).
  `--approve` supplies the same default as apply; node/use declarations win.
  This is an assessment, **not approval**. As in apply, output changes and reads
  do not by themselves violate `approve=none`.
- `input_basis` with source node, output, destination input, and `live` or
  opted-in `snapshot` provenance, without values. Unavailable bootstrap evidence
  can have source `unavailable`. `limitations` explain why existing upstream
  outputs cannot predict values after a later upstream apply.
- Structured diagnostics with code, category, severity, phase, subject, message, and remedy.
  Preparation failures also produce JSON before nodes start when possible.
  Source locations retained as HCL diagnostics appear in an optional `source` object
  (file, line, column); other existing parser context remains in messages.

Standalone inspection never applies. Artifacts use the same `.terragraph/plans/`
permissions and cleanup as apply, including failures and supported cancellation.
A subsequent apply still creates and inspects a fresh plan and applies those
same bytes under the existing confirmation, policy, and lock rules. Neither
a JSON result nor a `pass` assessment authorizes it.

Review planning continues independent branches after failures. Dependents of a
failed selected node retain status `not run` with a dependency diagnostic;
their counts and change verdict remain unknown. A missing output is distinct
from credential, provider, and live-read failures. No placeholder values are
injected. Snapshot fallback remains opt-in and is reported explicitly.
This scheduling change applies to CLI plan review only; apply/destroy still stop
before subsequent levels after a failed level.

Saved-plan inspection is unavailable for remote/cloud execution backends.
JSON reports `inspection_unsupported` and exits nonzero; text keeps native plan
preview and clearly reports that structured evidence is unavailable. Runtime
plan streams go to stderr in both formats; concise Terragraph summaries go to
stdout. A policy block is a successful assessment and does not itself fail plan;
preparation, input, provider, inspection, and cancellation errors exit nonzero.


Execution attempts now have protected [execution records](executions.md), including unknown-outcome recovery. Ordinary apply does not retain a plan bundle unless `--retain-plan` is selected. `plan --save` and `apply --plan <run-id>` expose separate review of one ready graph frontier at a time; continuation plans downstream nodes only after real upstream outputs exist. See the execution record reference for compatibility checks, shared storage, expiry, and cleanup.

### Runtime contracts and saved plans

Explicit [contracts](contracts.md) check actual producer values and effective
consumer inputs before downstream apply. Consumer checks use the selected
variables in the exact saved plan, including external inputs and module default
and conversion rules. Known output violations stop producer apply; computed
outputs are checked afterward, before snapshot publication or success recording.
A violation after mutation leaves the execution `applied` for output recovery,
never automatic replay. Contract meaning and mode participate in retained-plan
bindings. Review JSON exposes independent conditions through `review.contracts`.
Known null is reconstructed only from the same successful plan, never from a
missing live output. See the contracts reference for restart and recovery limits.

### Per-node deadlines

`--node-timeout 5m` on plan/apply/destroy limits each node action independently; 0 (the default) disables it. The deadline begins after dispatch and includes input resolution, init, planning, mutation, and output collection. Queue time, graph/runtime preflight, and lock acquisition are excluded. Saved frontier planning and `apply --plan` use the same per-node boundary and keep their existing membership, policy, and recovery rules. Apply and destroy require `--auto-approve` when deadlines are enabled.

A deadline does not establish whether infrastructure changed. The existing journal records observed outcomes and retains indeterminate mutations for explicit recovery; timeout never retries a mutation. Successfully completed actions retain their success even if cancellation arrives at the completion boundary. Subprocess cleanup can extend elapsed time beyond the deadline: Unix waits for its process group and Windows assigns timed runtimes to a non-breakaway job before they execute, then waits for tracked processes to terminate before releasing the action slot or graph lock.
