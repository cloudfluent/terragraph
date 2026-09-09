# First-party debug plugin

This observer lives in the terragraph repository and uses only the public Go SDK. It has no privileged engine access and follows the same explicit install, version lock, process isolation, and failure policy as third-party plugins.

Build a package from the repository root:

```sh
go run ./tools/pluginpackage --out /tmp/terragraph-debug-package
```

The output directory must not already exist. The tool builds for the current platform. Package version `0.1.0` and executable protocol compatibility are independent of the terragraph CLI release version.

Declare it in the root blueprint:

```hcl
plugin "debug" {
  source  = "terragraph/debug"
  version = "0.1.0"
  config = {
    level = "debug"
  }
}
```

Install explicitly, then use normal commands:

```sh
terragraph plugin install debug /tmp/terragraph-debug-package
terragraph --log-level debug validate
terragraph --log-level debug plan
terragraph --log-level debug apply --auto-approve
```

On Windows, choose an appropriate local directory for `--out` and installation. The descriptor and executable filename include the platform's `.exe` suffix automatically.

The plugin logs event identity, operation, phase, node, status, execution ID, and node counts. It never logs reference values, inputs, output values, plan contents, environment variables, or credentials. Set `level = "info"` to observe events with `--log-level info`. Diagnostics go to stderr; JSON command results remain on stdout.

Only transitions the engine actually reaches are logged. Early bootstrap failures remain core diagnostics. A rejected plan has no mutation event, unselected nodes have no fabricated execution events, and process crashes cannot guarantee terminal event delivery. Static evaluation and runtime execution use separate plugin processes. Logs are for debugging, not durable audit delivery.
