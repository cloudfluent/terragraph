# Native node operations

`run` executes one supported native command for one exact expanded leaf:

```sh
terragraph run --node checkout.database -- init
terragraph run --node checkout.database -- state list
terragraph run --node checkout.database -- state show 'aws_db_instance.main'
terragraph run --node checkout.database -- state pull
terragraph run --node checkout.database -- console
terragraph run --node checkout.database -- import 'aws_db_instance.main' existing-id
terragraph run --node checkout.database -- state mv 'aws_db_instance.old' 'aws_db_instance.main'
terragraph run --node checkout.database -- state rm 'aws_db_instance.old'
```

The node supplies its runtime, source directory, environment, backend configuration, and isolated `TF_DATA_DIR`. A group name is not expanded. `--` is required between terragraph flags and the native command. Native stdout, stderr, stdin, and positive exit codes are preserved; stdout is not wrapped in a terragraph JSON envelope.

| Native command | Preparation | Effect |
|---|---|---|
| `init` | Explicit backend initialization with `-reconfigure`, `-input=false`, and `-lockfile=readonly` | Journaled as potentially changing work; cannot migrate state or rewrite the provider lockfile |
| `state list`, `state show`, `state pull` | No automatic init or variable file | Read native state, including during unresolved execution recovery |
| `console` | Resolve this node's inputs and pass a temporary variable file when needed | Interactive native evaluation; no automatic init |
| `import` | Resolve inputs, disable variable prompts, keep native state locking, select a protected backup path | Change this node's state; no automatic init |
| `state mv`, `state rm` | Keep native state locking and select a protected backup path; no variable file | Change this node's state; cross-state movement is not supported |

Before a command that does not initialize, terragraph checks a backend-context fingerprint written by its most recent initialization. Configuration changes, an externally reconfigured native cache, or an absent fingerprint require the explicit `run ... -- init` command. Ordinary plan/apply initialization also writes this fingerprint. This prevents a native command from quietly using an old backend cache after a blueprint change. The fingerprint is replaceable local cache metadata, not Terraform state or a shared infrastructure registry.

Commit a provider lockfile when the module needs provider dependencies before using explicit `run ... -- init`. Backend and provider credentials must be available through the node's environment or normal runtime credential mechanisms. A missing or broken unrelated leaf does not by itself hide an inspectable selected leaf; the selected source, input dependencies, and managed path separation must still be valid.

These are explicit operator commands. They do not produce an infrastructure action plan or apply a reviewed plan, and `approve` resource-action policy is not presented as authorization for native state editing. Use backend/IAM access controls for who may perform these operations. Native `apply` and `destroy` are refused here; use terragraph's graph commands for those operations.

## Arguments and coordination

`-no-color` is supported. `-lock-timeout=<duration>` is supported for import, console, and state mutations, and `-id=<value>` is supported for `state list`. Backend, workspace, state-file, source-directory, variable-file, backup, migration, and lock-disabling overrides are refused. Nonempty `TF_CLI_ARGS*` or `TF_LOG*` overrides are also refused because they can silently redirect the selected operation or write outside managed paths.

Each invocation holds the existing local lock and configured graph lock. Native state locking remains enabled for writes. Mutations are journaled before native execution; failure or interruption keeps a conservative unknown outcome and blocks later writes. Read commands remain available for inspection. No native command is automatically retried.

`state push`, cross-state movement, workspace management, native force-unlock, and arbitrary native commands are outside this interface. Use a separately reviewed backend-native procedure when those are needed. `terragraph force-unlock` manages the graph lock, not a Terraform state lock. For an unknown operation in a fresh environment, inspect the backend using its native tools before acknowledging recovery; a missing cache is not proof that the operation never ran.

## Native backups

When the runtime produces a backup, terragraph archives its exact bytes alongside execution records in the configured local or S3 store. Ordinary history reports `backup_available` without showing state values. Export only when needed:

```sh
terragraph plan show <run-id> --backup > protected-backup.tfstate
```

`--backup` emits raw native state and cannot be combined with `--output json`. State and backup output can contain secrets. Keep exported files private. A native backend may manage version history itself or produce no local backup; terragraph does not invent a successful backup or promise one in that case.

Backups remain until the resolved execution record's retention deadline, unlike completed plan bundles which are cleaned immediately. Unknown outcomes retain their evidence regardless of age. If archival fails, the protected scratch file at `.terragraph/backups/<run-id>.tfstate` is preserved and its path is included in the error; preserve that file before discarding an ephemeral runner. `plan prune` retries eligible cleanup. Exporting a backup never pushes it into a backend or rolls anything back automatically.
