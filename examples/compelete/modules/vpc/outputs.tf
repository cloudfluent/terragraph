# Native import records only an ID; apply must restore fixture values before consumers can run.
output "network" {
  description = "Simulated network recorded by the local fixture."
  value       = try(terraform_data.this.output.network, null)
}
