# Executable plugin example

This example builds an optional string identity function using the public Go SDK. It does not run Terraform or alter resources. See [plugin support](../../docs/plugins.md) for current limitations and the broader lifecycle design.

From the repository root, on macOS or Linux:

```sh
mkdir -p /tmp/terragraph-echo-package
go build -o /tmp/terragraph-echo-package/plugin-echo ./examples/plugins/echo
go run ./examples/plugins/echo --descriptor > /tmp/terragraph-echo-package/plugin.json
```

On Windows, build the executable as `plugin-echo.exe`; generating the descriptor on Windows selects that filename automatically. For cross-compilation, the descriptor's `executable` must match the target filename.

In an existing blueprint with a string input named `name`:

```hcl
plugin "example" {
  source  = "local/echo"
  version = "1.0.0"
}

node "application" {
  source = "./module"
  vars = {
    name = example_value("application")
  }
}
```

Run these commands from that blueprint's directory:

```sh
terragraph plugin inspect /tmp/terragraph-echo-package
terragraph plugin install example /tmp/terragraph-echo-package
terragraph plugin list
terragraph --log-level info validate
```

Commit `terragraph.plugins.lock.json`. Another machine restores the exact package using `plugin install example PACKAGE_DIRECTORY --locked`; it needs an entry for its OS and architecture. The `source` is a reviewed package identity, not an automatically fetched URL.

The example sends an info-level progress message through `plugin.Logger(ctx)`. It appears on stderr with `plugin=example`, `feature=value`, and `phase=config.evaluate`; the message is hidden at the default warn level. Adding `--output json` to validate keeps stdout as a clean JSON result. The logger records only the argument count, not the input value.
