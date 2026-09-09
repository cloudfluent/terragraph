# Executable plugins

Plugin support is experimental. The executable SDK, local package installation, platform-specific version locks, isolated RPC sessions, pure functions in `vars`, and live structured SDK logs are implemented.

**Runtime lifecycle hooks are not active yet.** Infracost gates, observers, input resolvers, credential providers, leases, expansions, plugin reports in execution records, and remote package fetching remain unimplemented. A package declaring these features is rejected during loading, so a configured policy cannot silently disappear. SDK fields reserved for these contracts do not imply engine support. Do not treat the experimental SDK as a stable compatibility guarantee.

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

Static sessions exist only while configuration and referenced groups are evaluated. The resulting graph carries ordinary input values. No plugin receives mutable engine objects, Terraform plan paths, or an API for invoking Terraform. Function purity is an author contract, not something an external process can be proven to obey.

The host checks executable hashes before launch, uses mutually authenticated gRPC, preserves typed values and exact numbers, and supplies no ambient environment credentials or stdin. Raw plugin stdout, stderr, panic text, transport failures, and handler errors are not forwarded as diagnostics. Functions returning values marked sensitive are rejected; functions are not a secret injection path. Plugins must not put secrets in descriptor fields or ordinary values.

Startup and configuration have 10-second limits; function calls have a 2-second limit including time spent waiting for another call. Cancellation before dispatch leaves the session usable. A handler error fails the evaluation. A panic reported as fatal, a transport error, or a timeout after dispatch quarantines and terminates the session. Safe error codes distinguish queue cancellation, provider faults, transport failures, timeouts, and oversized messages. `Retryable` is metadata and never triggers automatic retries. It is never restarted within the evaluation. Terraform has not started at this point, so this failure does not trigger mutation recovery or replay. Cleanup kills the Unix process group or closes the Windows kill-on-close Job Object, and removes the unique session work directory. Removal failures are returned with the remaining path; another cleanup attempt is allowed. Process termination and bounded log draining can add time after the call deadline.

Process separation is not an OS sandbox. Installed code still has the user's filesystem and network access and must be trusted. A Unix program can deliberately leave its process group; the process cleanup guarantee covers ordinary child processes, not hostile code escaping containment. Windows launch is suspended until assignment to its Job Object succeeds.

Execution history and `force-unlock` read core metadata without loading plugins. Graph-based execution recovery may still need functions to reconstruct the graph. The editor offers plugin block attributes without executing plugins; descriptor-driven function completion is not implemented yet.

## Common SDK Logger

The current executable protocol is **2**, which requires the live log stream. Rebuild and reinstall packages built with protocol 1; the package lock format remains version 1. Package versions, executable protocol versions, and lock format versions are independent.

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

Feature requests and responses retain their 4 MiB bound. Oversized requests are rejected before dispatch without poisoning the session; oversized responses are classified separately and quarantine the session. Large-plan artifact streaming, lease renewal scheduling, durable external-effect receipts, and runtime observer/gate policies remain part of the unimplemented lifecycle work described above.

## Managed files

- `terragraph.plugins.lock.json`: reviewed, platform-specific package selection.
- `.terragraph/plugins/packages/<digest>/<platform>/`: installed descriptor and executable.
- `.terragraph/plugins/packages/<digest>/.install-*`: temporary publication directories removed after installation.
- `.terragraph/plugins/work/evaluation-*/<alias>/`: unique static session working directory, removed on normal termination.
- `.plugins-lock-*`: temporary root-level lock publication file, removed after installation.

Package cache and session directories are private. Process crashes can leave temporary working files; no automatic deletion of another invocation's directory occurs. The RPC library also creates temporary local transport sockets under the OS temporary directory; it removes its transport directory on normal client shutdown. terragraph does not generate or modify `.tf` files or Terraform module directories.
