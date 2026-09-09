# Native import records only an ID; apply must restore fixture values before consumers can run.
output "transit" {
  description = "Simulated transit recorded by the local fixture."
  value       = try(terraform_data.this.output.transit, null)
}
