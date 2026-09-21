plugin "secretsmanager" {
  source  = "terragraph/secretsmanager"
  version = "0.1.0"
  config = {
    region = "us-east-1"
    # endpoint = "http://localhost:4566" # localstack: uncomment for local runs
  }
}

node "app" {
  source = "./modules/app"

  input "db_password" {
    from = plugin.secretsmanager.read
    ref  = { secret_id = "prod/app/db", json_pointer = "/password" }
  }

  # Immutable pinning: version_id binds one exact secret version (use version_stage
  # instead to pin a stage such as "AWSPENDING"); field selects a top-level JSON key.
  # input "api_key" {
  #   from = plugin.secretsmanager.read
  #   ref  = { secret_id = "prod/app/keys", version_id = "11111111-2222-3333-4444-555555555555", field = "api_key" }
  # }

  # Scoped credentials for this node's terraform subprocesses only. The environment
  # list must name exactly what authenticate returns; the host refuses otherwise.
  # credential "aws" {
  #   from        = plugin.secretsmanager.authenticate
  #   ref         = { role_arn = "arn:aws:iam::123456789012:role/terragraph-app" }
  #   environment = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]
  # }
}
