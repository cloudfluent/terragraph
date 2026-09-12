# First-party AWS Secrets Manager plugin

This plugin implements the production secret read path on the public Go SDK: an `input_resolver` that reads secret values (with immutable version pinning, landing next) and a `credential_provider` that leases AWS credentials. It has no privileged engine access and follows the same explicit install, version lock, process isolation, and failure policy as third-party plugins.

Build a package from the repository root:

```sh
go run ./tools/pluginpackage --plugin secretsmanager --out /tmp/terragraph-secretsmanager-package
```

The output directory must not already exist. The tool builds for the current platform. Package version `0.1.0` and executable protocol compatibility are independent of the terragraph CLI release version.

Declare it in the root blueprint:

```hcl
plugin "secretsmanager" {
  source  = "terragraph/secretsmanager"
  version = "0.1.0"
  config = {
    region   = "us-east-1"
    endpoint = "http://localhost:4566" # optional; localstack for tests
  }
}
```

`region` is required; `endpoint` is optional and intended for localstack in tests. Any other configuration key is rejected at validate time. Install explicitly, then use normal commands:

```sh
terragraph plugin install secretsmanager /tmp/terragraph-secretsmanager-package
terragraph validate
```

On Windows, choose an appropriate local directory for `--out` and installation. The descriptor and executable filename include the platform's `.exe` suffix automatically.

Both features are declared `read_only`: `GetSecretValue` and STS `AssumeRole` are idempotent reads, so the host may retry them without durable execution records. The plugin never logs secret values or credentials; responses carry only the typed sensitive value or the leased environment map. The `read` and `authenticate` actions are not implemented yet — they return explicit errors until the GetSecretValue resolver and the STS credential lease land in the next changes; `--descriptor` and blueprint-time configuration validation work today.
