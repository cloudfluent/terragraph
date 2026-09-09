# Native import records only an ID; apply must restore fixture values before consumers can run.
output "context" {
  description = "Simulated context recorded by the local fixture."
  value       = try(terraform_data.this.output.context, null)
}
