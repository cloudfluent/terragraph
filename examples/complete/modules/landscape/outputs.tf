# Native import records only an ID; apply must restore fixture values before consumers can run.
output "environments" {
  description = "Simulated environments recorded by the local fixture."
  value       = try(terraform_data.this.output.environments, null)
}

output "summary" {
  description = "Simulated summary recorded by the local fixture."
  value       = try(terraform_data.this.output.summary, null)
}
