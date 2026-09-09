# Native import records only an ID; apply must restore fixture values before consumers can run.
output "database" {
  description = "Simulated database recorded by the local fixture."
  value       = try(terraform_data.this.output.database, null)
}

output "credentials" {
  description = "Simulated credentials recorded by the local fixture."
  value       = try(terraform_data.this.output.credentials, null)
  sensitive   = true
}
