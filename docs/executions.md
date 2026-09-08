# Execution records

Execution records describe command attempts and confirmed results. They are not Terraform state, output snapshots, or proof that infrastructure is unchanged.

```sh
terragraph plan list
terragraph plan list --output json
terragraph plan show run-0123456789abcdef0123456789abcdef --output json
```

Both commands only inspect stored records. They do not run Terraform/OpenTofu, retry a failed operation, or initialize a backend. Records remain readable when a node's module is missing or its wiring is broken, provided the selected blueprint can still be parsed. An absent record is an error for `show`; it does not mean that nothing was applied. `list` returns an empty collection if this store has no records. It returns readable records newest first even if a sibling object is corrupt, with a diagnostic and nonzero exit status for the damaged object. `show` reads its requested ID directly. Foreign coordination-scope records remain visible with a diagnostic naming the conflict; inspection never silently hides records that would block a mutation.

The record format has its own `schema_version: 1`. JSON results use explicit public fields; private coordination and target fingerprints are not included. An argument, configuration, or storage failure emits a `diagnostics` array under `--output json` and exits nonzero. The existing node `status` command continues to observe Terraform state and does not read execution history.

Ordinary `plan`, `apply`, and `destroy` now record their attempts by default. Ordinary `apply` still uses a temporary runtime plan and does not package or upload a retained plan bundle. Failure to publish a required transition prevents the next mutation. Unresolved mutations block subsequent plan, apply, and destroy commands sharing this store.

## Storage configuration

The default is the protected local `.terragraph/executions/` directory. No S3 configuration is required for local use. An optional root-level block changes storage and retention settings:

```hcl
execution {
  plan_ttl         = "24h"
  record_retention = "720h"
}
```

For shared records, configure all three S3 location fields and the same graph lock on every executor:

```hcl
execution {
  bucket = "example-artifacts"
  prefix = "payments/production/executions"
  region = "us-east-1"
}

lock {
  s3 {
    bucket = "example-artifacts"
    key    = "payments/production/graph.lock"
    region = "us-east-1"
  }
}
```

`bucket`, `prefix`, and `region` must be nonempty literal strings and supplied together. The prefix must be dedicated to execution artifacts and separate from state and the lock object. S3 storage requires a graph lock; changing artifact storage does not change the Terraform backend. AWS credentials use the normal SDK credential chain and are not written into execution records by this configuration.

`plan_ttl` and `record_retention` are positive duration strings, defaulting to 24 and 720 hours respectively. Plan expiry controls whether a retained plan may begin execution. Record retention starts only after a resolved execution finishes. Neither setting stops a running operation or proves a crashed executor has stopped. These settings do not install a cleanup daemon or change bucket Lifecycle rules.

Records and optional retained-plan objects share this storage configuration, but are separate objects. Recording an attempt does not require packaging a retained plan. Mutating decisions hold the existing local lock and configured graph lock. S3 conditional updates and deletion also check the previous object revision. The SDK response ETag is the S3 concurrency token; an embedded object nonce is not a token for offline updates.

Local data is written to a private pending file, flushed, and renamed before publication. POSIX directories are then synced. Windows flushes file contents but does not provide the same directory-fsync guarantee; missing data after a crash must never be interpreted as a successful or never-started operation. Local pending files are not executable artifacts.

## Interpreting phases

- `pending` means no execution start has been recorded for that node.
- `initializing`, `applying`, and `operating` mean a potentially changing command has started or was about to start; interruption requires investigation.
- `applied` means application succeeded but output collection or other post-processing has not been recorded as complete.
- `completed` means the node's operation and required post-processing completed.
- `indeterminate` means the runtime result cannot establish the full effect of the operation.

A record with an unresolved mutation is `needs_recovery`, not an ordinary retry instruction. Do not reapply a saved plan merely because a previous process exited or a record has no completion timestamp. Infrastructure changes and recording their outcome are not a single transaction.

## Recovery

First inspect `plan show <run-id>` and verify that the previous process or CI job has stopped. A timeout or released graph lock alone is insufficient. Never force-unlock a live executor.

When the record says `applied`, retry only output collection:

```sh
terragraph plan recover <run-id> --confirm-stopped
```

This checks the configured target and reads outputs without plan or apply. It prepares an isolated temporary backend cache with a read-only provider lockfile, then removes the cache. Default-workspace local and HTTP backends have verified read-only preparation. Other backends or workspaces require explicit `--initialize-backend`; that preparation is journaled as potentially changing work, and interruption keeps the recovery barrier. Restore the original backend configuration if it changed. No downstream nodes start automatically.

Mixed records recover readable `applied` siblings even when unknown peers remain. The command returns a nonzero status for unresolved peers; `plan show` displays the completed output collection. The recovery barrier remains until those peers are inspected and retired. Backend preparation uncertainty is recorded separately so a previously confirmed apply remains confirmed.

When the outcome is unknown, inspect the real backend state and affected resources using your normal runtime/backend tools. After reconciling any partial changes, explicitly retire the attempt:

```sh
terragraph plan recover <run-id> --confirm-stopped --state-reviewed --replan
```

The command preserves the original node outcome, records recovery separately, and requires a fresh plan. It does not label an unknown operation successful, roll back changes, release native state locks, or prove what an external writer did. The acknowledgements are operator assertions, not automated verification. The replan path remains available with missing module sources so damaged checkouts cannot permanently hide recovery metadata.

## Saved graph plans

Use the same execution mechanism in two ways:

```sh
terragraph apply --auto-approve
terragraph apply --auto-approve --retain-plan
```

Both commands plan each node, inspect its policy, apply the same native plan, and use real outputs for downstream nodes. The first keeps only the execution record. The second additionally persists private plan artifacts while the run is in progress; it does not pause for a separate review. Resolved terminal runs immediately attempt artifact cleanup, so retention is not a long-term plan archive.

For a separate approval step:

```sh
terragraph plan --save --output json
terragraph plan show <run-id> --output json
terragraph apply --plan <run-id> --auto-approve
terragraph plan --save --continue <run-id> --output json
terragraph apply --plan <run-id> --auto-approve
```

The first command saves only the ready frontier. A downstream node whose selected predecessor has not completed stays `pending`. Applying consumes that frontier once and stops. If a later peer fails before mutation (for example, approval policy refuses it), already completed peers stay completed and unapplied plans remain available under `waiting_for_apply`. A retry applies only those remaining peers; expired or incompatible plans still require cancellation and a fresh plan. Unknown mutation outcomes keep the recovery barrier and all evidence. `--continue` plans the next frontier using live upstream outputs; snapshots cannot replace those reads. Repeat until the record says `completed`. Use `--node` only when starting an explicitly restricted selection; continuation and saved apply inherit the stored selection. Parallelism is an upper bound; saved frontiers currently execute sequentially, while ordinary apply retains its existing concurrent scheduler.

Saved-plan JSON includes action metadata and the policy assessment without input or output values. Review is not approval: saved apply checks the current approval policy again and asks for confirmation unless `--auto-approve` is set. A saved no-change plan still goes through native application validation, so it cannot silently bypass the runtime's stale-plan checks.

The private bundle contains the native plan, an immutable plan ID, its execution and node association, creation and expiry times, and a compatibility fingerprint. It may contain secrets, including values that Terraform marks sensitive. Keep store access restricted; do not put these artifacts in Git or general-purpose logs.

Before applying, terragraph compares backend/workspace identity, runtime version and platform, provider selections, absolute module and data-directory paths, source files, resolved inputs, and `TF_VAR_*` inputs. Runtime argument overrides via `TF_CLI_ARGS*` are refused for retained plans. The runtime itself validates the state revision embedded in its native plan while holding its state lock. Terragraph does not replace that check with a local state cache or silently generate a replacement plan.

Transporting a bundle through S3 does not make native plans universally portable. Supply the same source files and provider lockfile at the same absolute paths, select the same runtime, and provide valid backend/provider credentials. Backend preparation is journaled and may reconstruct the native cache; it never replans. Source-tree symlinks and oversized source trees are refused for retained plans. Native plans can reference files outside the source tree, executable helpers, or apply-time provisioner inputs: reproduce those dependencies in the runner environment. Terragraph does not package that environment or prove arbitrary external dependencies unchanged. This follows [Terraform's automation requirements](https://developer.hashicorp.com/terraform/tutorials/automation/automate-terraform). Terraform and OpenTofu plans are not interchangeable.

A common graph lock and execution store are required for shared coordination. Git-only teams can keep local records for independent Terraform states. Teams sharing infrastructure should configure the same lock and S3 execution prefix, whether commands run on developer machines, a central CI runner, or a custom pipeline. Separate lock/store namespaces cannot automatically discover each other. Terraform backend locking remains necessary, including for writers outside terragraph.

## Expiry and cleanup

```sh
terragraph plan cancel <run-id>
terragraph plan prune --output json
```

The default start window is 24 hours. Expiry prevents a stored plan from beginning; it does not terminate an operation already running. Under the coordination locks, pruning expires paused frontiers whose plan window has elapsed, marks the execution terminal, and removes their bundles. Completed, cancelled, expired, or otherwise resolved terminal executions also attempt immediate bundle cleanup. Failed cleanup can be retried by the next ordinary plan/apply/destroy invocation or by `plan prune`.

Resolved records remain for 30 days after their terminal timestamp by default. Active records, unresolved mutations, uncertain backend preparation, and orphan objects without trustworthy ownership are never deleted merely because they are old. Cancellation cannot resolve an unknown mutation; use the recovery procedure first. There is no background daemon, so an idle local checkout keeps eligible files until another relevant command runs.

S3 deletion affects the current object view. A versioned bucket can retain older versions and delete markers, and Object Lock can prevent physical removal. Configure and audit any noncurrent-version policy separately; do not apply blanket Lifecycle expiration to the execution prefix or graph lock. A bucket policy that independently deletes active or unknown evidence defeats these recovery guarantees. The CLI does not install or change bucket Lifecycle rules.
