# Native import records only an ID; apply must restore fixture values before consumers can run.
output "addons" {
  description = "Simulated addons recorded by the local fixture."
  value       = try(terraform_data.this.output.addons, null)
}
