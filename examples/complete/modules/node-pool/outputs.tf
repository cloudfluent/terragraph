# Native import records only an ID; apply must restore fixture values before consumers can run.
output "pool" {
  description = "Simulated pool recorded by the local fixture."
  value       = try(terraform_data.this.output.pool, null)
}
