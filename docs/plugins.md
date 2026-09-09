# Executable plugins

Plugin support is experimental. The executable SDK, local installation and version locks, pure HCL functions, static node expansion, graph validators, lifecycle observers and gates, deferred inputs, credential leases, and structured SDK logs are supported.

The host owns scheduling, plan identity, admission, cancellation, and recovery. Plugins return values, decisions, and reports; they cannot request Terraform commands or modify the graph during execution. The executable protocol is **3**. Rebuild older experimental packages before installing them; this is not a stable SDK compatibility promise.

## Declare and install

```hcl
plugin "example" {
  source  = "local/echo"
  version = "~> 1.0"
  config  = {}
}

node "application" {
  source = "./module"
  vars = {
    name = example_value("application")
  }
}
```

Declarations belong in the root blueprint. The alias prefixes every function as `<alias>_<feature>`. Configuration and package selection remain literal values. Functions are available in node and use `vars`, including nodes inside referenced groups. Other attributes remain literal or use their existing dedicated reference syntax. There are no native locals, implicit node references, or added iteration syntax; dependency values still travel through `edge`.

A release package is a directory containing `plugin.json` and the executable named by that descriptor. The executable must use the public [`plugin`](../plugin) Go package or implement its protocol. The [buildable example](../examples/plugins) demonstrates both files.

```sh
terragraph plugin inspect PACKAGE_DIRECTORY
terragraph plugin install example PACKAGE_DIRECTORY
terragraph plugin list
```

Installation is explicit and serialized under the blueprint lock. Commands that take this lock, including `plan`, `apply`, `destroy`, and `vendor`, acquire it before static plugin evaluation and retain it through session cleanup. It copies and verifies the package, then writes the selected version, source identity, platform, and SHA-256 digest to `terragraph.plugins.lock.json`. Commit that file. The package digest binds both descriptor and executable; RPC startup checks that the running descriptor agrees with the installed descriptor. A checksum verifies reviewed bytes, not publisher identity or safety.

`plugin install example PACKAGE_DIRECTORY --locked` restores an existing platform entry without changing the lock. Reinstallation replaces damaged cached package bytes only after verifying the supplied replacement. Install a new compatible package without `--locked` to update that entry, and review the lock diff. Each OS/architecture has a separate entry; no automatic cross-platform download is performed. At present `source` is an identity recorded in the lock, not a registry or URL resolver.

Ordinary graph commands never install, fetch, or update plugins. Missing packages, changed constraints, changed source identities, and checksum mismatches stop loading with a remedy.

## Execution and failure behavior

Static sessions exist only while configuration and referenced groups are evaluated. Runtime sessions are separate and remain available through command completion and credential release. The resulting graph carries computed static values and explicit deferred input bindings. No plugin receives mutable engine objects, Terraform plan paths, or an API for invoking Terraform. Function purity is an author contract, not something an external process can be proven to obey.

The host checks executable hashes before launch, uses mutually authenticated gRPC, preserves typed values and exact numbers, and supplies no ambient environment credentials or stdin. Raw plugin stdout, stderr, panic text, transport failures, and handler errors are not forwarded as diagnostics. Functions returning values marked sensitive are rejected; functions are not a secret injection path. Plugins must not put secrets in descriptor fields or ordinary values.

Startup and configuration have 10-second limits; function calls have a 2-second limit including time spent waiting for another call. Cancellation before dispatch leaves the session usable. A handler error fails the evaluation. A panic reported as fatal, a transport error, or a timeout after dispatch quarantines and terminates the session. Safe error codes distinguish queue cancellation, provider faults, transport failures, timeouts, and oversized messages. Runtime pure and read-only features may retry a nonfatal `Retryable` fault up to twice within the original deadline. Static function calls and external effects are not automatically retried. A fatal session is never restarted within the invocation. Static evaluation failures occur before Terraform starts. Runtime failures preserve the last recorded infrastructure outcome and may require plugin-effect recovery. Cleanup kills the Unix process group or closes the Windows kill-on-close Job Object, and removes the unique session work directory. Removal failures are returned with the remaining path; another cleanup attempt is allowed. Process termination and bounded log draining can add time after the call deadline.

Process separation is not an OS sandbox. Installed code still has the user's filesystem and network access and must be trusted. A Unix program can deliberately leave its process group; the process cleanup guarantee covers ordinary child processes, not hostile code escaping containment. Windows launch is suspended until assignment to its Job Object succeeds.

Execution history and `force-unlock` read core metadata without loading plugins. Graph-based execution recovery may still need functions to reconstruct the graph. The editor offers plugin block attributes without executing plugins; descriptor-driven function completion is not implemented yet.

## Feature configuration and access

A plugin exports named features. Declaring a plugin enables its exports; `feature` blocks refine a feature's mode and deadline. Unknown feature names fail loading.

```hcl
plugin "policy" {
  source  = "example/policy"
  version = "1.0.0"

  feature "cost_limit" {
    mode    = "enforce"
    timeout = "60s"
  }

  access {
    environment = ["POLICY_TOKEN"]
    plan        = true
  }
}
```

Gates and validators default to `enforce`; `advisory` reports a warning without blocking. Observers default to `best_effort`; `enforce` makes delivery a required postcondition and requires an execution record. Input and credential suppliers are required when a binding uses them. Deadlines must be positive and at most five minutes, and include call queue and startup time. `configure` validates configuration without making external requests.

`access.environment` forwards only the listed environment values to runtime plugin processes, never static processes. `access.plan` explicitly permits gates to receive the native plan document, which can contain secrets. Observers receive metadata, not plan contents, resolved inputs, or output values. Graph evidence contains node names, sources, input names, and dependencies. Process separation is not an OS sandbox.

## Lifecycle and failure policy

| Phase | Meaning |
|---|---|
| `config.evaluate`, `config.expand`, `graph.validate` | Static evaluation, atomic expansion, and graph evidence after core validation |
| `selection.ready`, `run.prepare` | Selected scope and preparation after the execution journal exists |
| `observation.prepare` | Read session setup without creating an infrastructure execution |
| `source.vendor.before`, `source.vendor.after` | Selected source publication and its result |
| `node.prepare`, `input.resolve`, `credential.acquire` | Node dispatch, deferred input retrieval, and scoped credentials |
| `node.init.before`, `node.init.after` | Actual native initialization |
| `node.plan.ready` | Native plan evidence, or explicitly `text_only` when no saved-plan assessment is required |
| `node.mutation.admit`, `node.mutation.finished` | Final authorization and the journaled infrastructure outcome |
| `node.outputs.ready` | Output collection after core contract validation |
| `node.read.before`, `node.read.after` | Actual output or state reads |
| `native.operation.before`, `native.operation.after` | Other native subprocess operations, identified by command class without raw arguments |
| `node.finished`, `level.finished`, `run.finished` | Terminal node results, level completion, and scheduling completion |
| `credential.renew`, `credential.release`, `session.close` | Lease maintenance and bounded shutdown |

Phases are semantic transitions, not callbacks around every Go function. Unreached phases are not fabricated. Terminal node events cover skipped selected nodes as well as executed nodes. Bootstrap failures before an observer can start remain core diagnostics. Logs are not themselves re-emitted as lifecycle events.

A gate must return `allow`, `deny`, or `unknown`. Required deny, unknown, expiry, timeout, or malformed responses prevent the dependent operation. `--auto-approve` cannot bypass a gate. Plan gates inspect the same native bytes that will be applied and are reevaluated immediately before mutation with `metadata.recheck = "true"`; core approval rules still apply. A command without plan evidence cannot bypass a required plan gate. Destroy uses a saved destroy plan when a plan gate is configured.

An observer cannot change an infrastructure outcome. Required delivery failures produce a nonzero command result while completed node phases remain completed. Unresolved required external effects retain a recovery barrier. Optional observer failures produce warnings. Fatal errors quarantine the process; future calls that require that process fail, while optional observer failure alone does not cancel infrastructure work. Same-level execution retains the engine's existing failure behavior: already-started independent work finishes, and ordinary runs stop before the next level.

## Deferred inputs and credentials

```hcl
node "database" {
  source = "./modules/database"

  input "password" {
    from = plugin.secrets.read
    ref  = { path = "applications/database", field = "password" }
  }

  credential "provider" {
    from        = plugin.secrets.authenticate
    ref         = { account = "production" }
    environment = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]
  }
}
```

`input` and `credential` are core node bindings, including nodes inside groups. `ref` is literal provider-specific data. Bindings identify an exact exported feature. An input cannot also be supplied by `vars`, a data edge, or an exported group input. Resolver values are treated as sensitive, and the module variable must declare `sensitive = true` so Terraform also protects its presentation. The host type-checks the value and rechecks any `expires_at` before mutation. Graph and validation commands do not resolve these references.

Credentials are supplied only to the named node's subprocesses. Return a stable, non-secret `identity` describing the authenticated target; saved-plan verification compares that identity without recording token bytes. Returned environment names must appear in the binding's allowlist. Runtime argument overrides, data-directory controls, workspaces, and other routing controls are refused. Credential tokens are never installed in the host process's environment.

An optional lease contains an ID, expiry, and renewal time. Renewal maintains the same credentials and identity; replacing an environment map cannot update an already-running subprocess. Renewal failure cancels the dependent node context and prevents new dependent work. Credential-dependent Windows subprocesses use kill-on-close Job Objects; ordinary native console behavior remains unchanged. The host releases execution leases only after subprocesses have returned, under the still-held locks. Module-input secrets are not revoked at command exit.

## Reports and recovery

Plugin calls in execution records retain feature identity, package and configuration fingerprints, plan identity, decisions, and delivery status. Sensitive return values and credential tokens are omitted. Records containing plugin contracts use schema version 2; readers still accept version 1. Public history summaries expose call IDs and statuses, not private lease references or reports. History JSON uses schema version 2 when plugin call summaries are present, while plugin-free output retains its existing shape.

```sh
terragraph plan show RUN_ID
terragraph plugin report RUN_ID CALL_ID
terragraph plugin recover RUN_ID CALL_ID --confirm-stopped
terragraph plugin recover RUN_ID CALL_ID --confirm-stopped --acknowledge-external-state
```

Report export is explicit because plugin-authored reports can contain plan information. Recovery replays only recorded idempotent observer deliveries or known credential release work, using the original logical key and matching package/configuration. It never runs plan, apply, destroy, or import. Non-idempotent or otherwise unreplayable outcomes require external inspection and explicit acknowledgement; acknowledgement is recorded as such, not fabricated delivery success. Infrastructure recovery cannot clear unresolved plugin effects.

Output recovery reacquires declared credentials in a dedicated credential-only session and journals lease cleanup. It does not replay the original run’s gates or notification effects. A credential feature with an explicit operation filter must include `recovery` to serve this path.

Without an execution record, credential recovery evidence stays in the protected invocation work directory and cleanup failure reports that path. These receipts require operator inspection; execution-ID recovery commands do not operate on them. Unresolved work is not automatically pruned.

## First-party plugins

The [debug observer](../plugins/debug/README.md) is maintained in this repository and built with `go run ./tools/pluginpackage --out PACKAGE_DIRECTORY`. It uses the public SDK and the same install and lock flow as third-party packages. It records lifecycle metadata through the shared logger without reading secrets or plan contents.

Remote registries and automatic downloads remain outside the local package installer. No cloud-service-specific client library is part of the lifecycle host.

## Common SDK Logger

The current executable protocol is **3**, which includes lifecycle payloads and bounded plan streaming. Rebuild and reinstall packages built with protocols 1 or 2; the package lock format remains version 1. Package versions, executable protocol versions, and lock format versions are independent.

Inside a handler, use the standard `slog` interface supplied by the SDK:

```go
plugin.Logger(ctx).Info("request completed",
    "item_count", 3,
    "credential", plugin.Secret(token),
)
```

Pass this logger to libraries accepting `*slog.Logger`. `With` and `WithGroup` preserve the call identity. `Secret` discards its argument before serialization; it does not retain or send the original value. Arbitrary secrets embedded in message strings or unmarked attributes cannot be automatically identified. The logger is scoped to the active handler context; there is no process-global logger that can accidentally attribute parallel calls to each other.

A dedicated gRPC stream delivers progress while the handler is still running. It does not wait for the feature response. The host adds the configured plugin alias, invocation ID, fresh call ID, feature, and available node and phase information. Static functions currently identify the feature and `config.evaluate` phase; their individual HCL node location is not available in the function callback. Plugin-supplied fields use a `fields.` prefix and cannot replace host provenance. Diagnostics are not re-emitted as lifecycle events.

`--log-level` controls both engine and plugin diagnostics. Debug and info messages remain hidden with the default warn level. CLI bootstrap installs the logger and cancellation context before configuration evaluation, including vendor and observation commands. Embedders can use `plugins.WithLogger` with `engine.LoadContext` or `LoadLockedContext`. Raw stdout and stderr mirroring remains disabled; libraries writing directly to those streams should be adapted to the SDK logger.

Logs use stderr through the existing host logger. `--output json` continues to write only the command result on stdout. An error-level log is not a failed feature response and cannot abort infrastructure work.

Both SDK and host queues hold at most 256 records. Text fields are capped at 2,048 bytes plus a truncation marker, with at most 16 attributes; terminal control characters are replaced before output. The log stream has a 256 KiB frame limit. The host additionally accepts at most 2,000 records per second per session. Overflow is non-blocking and reported with `log_dropped`; interrupted delivery is reported as `log_stream_failed`. Diagnostics can be lost on abrupt process or host termination; these logs are not a durable audit journal.

Shutdown allows 100 ms for completion markers and another 100 ms for the output worker to drain, in addition to process termination. Slow output does not block feature RPCs or wait indefinitely during session shutdown. An already-blocked custom `slog.Handler` or writer cannot be forcibly interrupted by Go; its worker can finish when the writer becomes available. A blocked output destination cannot display its own failure immediately.

Feature requests and responses retain their 4 MiB bound. Oversized requests are rejected before dispatch without poisoning the session; oversized responses are classified separately and quarantine the session. Plan documents use a separate client stream with 1 MiB chunks and a 64 MiB total limit. Oversized evidence fails the check rather than being truncated.

## Managed files

- `terragraph.plugins.lock.json`: reviewed, platform-specific package selection.
- `.terragraph/plugins/packages/<digest>/<platform>/`: installed descriptor and executable.
- `.terragraph/plugins/packages/<digest>/.install-*`: temporary publication directories removed after installation.
- `.terragraph/plugins/work/evaluation-*/<alias>/`: unique static function session working directory, removed on normal termination.
- `.terragraph/plugins/work/runtime-*/<alias>/`: private lifecycle session directories. Without an execution journal, `receipts.json` and atomic `.receipt-*` scratch preserve unresolved credential cleanup evidence under the invocation directory.
- `.plugins-lock-*`: temporary root-level lock publication file, removed after installation.

Package cache and session directories are private. Process crashes can leave temporary working files; no automatic deletion of another invocation's directory occurs. The RPC library also creates temporary local transport sockets under the OS temporary directory; it removes its transport directory on normal client shutdown. terragraph does not generate or modify `.tf` files or Terraform module directories.
