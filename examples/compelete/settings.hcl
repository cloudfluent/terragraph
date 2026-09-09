# Runtime selection falls back to Terraform, or --tofu; named overrides are a separate lab.
contracts { mode = "enforce" }
snapshots {}
tfvars { location = "workdir" }
execution {
  plan_ttl         = "2h"
  record_retention = "168h"
}
