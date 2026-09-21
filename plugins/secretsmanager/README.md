# First-party AWS Secrets Manager plugin

This plugin implements the production secret read path on the public Go SDK: an `input_resolver` that reads secret values with property and immutable version selection, and a `credential_provider` that leases AWS credentials. It has no privileged engine access and follows the same explicit install, version lock, process isolation, and failure policy as third-party plugins.

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

Read a secret from a node input binding. The reference accepts `secret_id` (required), `version_id` or `version_stage` (optional; an explicit `version_id` pins an immutable version and wins over the stage, which defaults to `AWSCURRENT`), and exactly one of `json_pointer` (RFC 6901 pointer into the secret's JSON) or `field` (top-level JSON key):

```hcl
node "db" {
  source = "./modules/db"
  input "password" {
    from = plugin.secretsmanager.read
    ref  = { secret_id = "prod/db", json_pointer = "/password" }
  }
}
```

The selected value returns as a sensitive string (numbers and booleans render as their JSON text; a secret with no selector returns its full text). AWS errors map to safe fault codes — `not_found`, `access_denied`, `throttled` (retryable), `aws_request_failed` (fatal) — without echoing AWS error detail, and the value is never logged. Binary secrets (`SecretBinary`) are unsupported in v1; store the payload as `SecretString` JSON.

Lease AWS credentials for a node's own subprocesses with `authenticate`. Its reference accepts `role_arn` (optional; omit to use the chain credentials directly), `profile` (optional shared-config profile), and `session_name` (optional; defaults to `terragraph-secretsmanager` so repeated acquires keep a comparable identity). It returns exactly `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN` — an absent session token is omitted — so the binding's `environment` allowlist must list those names, and a binding without the allowlist fails validation:

```hcl
node "db" {
  source = "./modules/db"

  credential "aws" {
    from        = plugin.secretsmanager.authenticate
    ref         = { role_arn = "arn:aws:iam::123456789012:role/terragraph" }
    environment = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]
  }
}
```

The returned `identity` is a non-secret principal ARN (the caller ARN, or the assumed-role ARN when `role_arn` crosses an STS AssumeRole hop); saved-plan verification compares it without recording token bytes. An assumed-role lease carries the STS expiration; the host stops the run at expiry rather than rotating credentials under an already-running subprocess.

Both features are declared `read_only`: `GetSecretValue` and STS `AssumeRole` are idempotent reads, so the host may retry them without durable execution records. The plugin never logs secret values or credentials; responses carry only the typed sensitive value or the leased environment map. A buildable blueprint using both features lives in [`examples/plugins/secrets`](../../examples/plugins/secrets/README.md), and an env-gated end-to-end test (`TG_E2E_AWS=1`, real AWS or localstack via `TG_E2E_ENDPOINT`) ships in this package.
