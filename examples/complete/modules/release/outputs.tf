# Native import records only an ID; apply must restore fixture values before consumers can run.
output "release" {
  description = "Simulated release recorded by the local fixture."
  value       = try(terraform_data.this.output.release, null)
}
