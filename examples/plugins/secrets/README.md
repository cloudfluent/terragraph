# Secrets Manager read path example

This example wires one node input to a real secret store: `db_password` is resolved by the first-party [`secretsmanager`](../../../plugins/secretsmanager/README.md) plugin at plan and apply time, never stored in the blueprint. It demonstrates property selection (`json_pointer`), immutable version pinning and scoped node credentials (commented in [`blueprint.hcl`](blueprint.hcl)), and the module-side `sensitive = true` contract. `validate` and `graph` never resolve the secret; only execution commands do.

Build and install the package from the repository root:

```sh
go run ./tools/pluginpackage --plugin secretsmanager --out /tmp/terragraph-secretsmanager-package
```

Then, from this directory:

```sh
go run ../../../cmd/terragraph plugin inspect /tmp/terragraph-secretsmanager-package
go run ../../../cmd/terragraph plugin install secretsmanager /tmp/terragraph-secretsmanager-package
go run ../../../cmd/terragraph validate
go run ../../../cmd/terragraph plan
```

`plan` resolves `prod/app/db` through `GetSecretValue`, so the secret must exist in the configured region first. Point `secret_id` at a secret you control before running.

Localstack instead of real AWS: set `endpoint = "http://localhost:4566"` in the plugin `config`, create the secret with static credentials, and keep the fake credentials in your environment for the run:

```sh
aws --endpoint-url http://localhost:4566 secretsmanager create-secret \
  --name prod/app/db --secret-string '{"password":"local-only"}'
```

The env-gated end-to-end test in `plugins/secretsmanager` exercises the built package — real descriptor, real host session — against real AWS or localstack. It skips by default:

```sh
TG_E2E_AWS=1 TG_E2E_SECRET_ID=prod/app/db TG_E2E_REGION=us-east-1 \
TG_E2E_ENDPOINT=http://localhost:4566 TG_E2E_SECRET_JSON_POINTER=/password TG_E2E_SECRET_VALUE=local-only \
go test ./plugins/secretsmanager/ -run TestE2E -v
```

Credentials come from your environment or shared config (`~/.aws`) via the AWS default chain; the plugin persists nothing. In a real project, commit `terragraph.plugins.lock.json` after reviewing the digest; this scratch example gitignores it because the digest binds your local build. See [plugin support](../../../docs/plugins.md) for the binding, allowlist, and failure rules.
