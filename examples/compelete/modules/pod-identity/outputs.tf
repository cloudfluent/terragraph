# Native import records only an ID; apply must restore fixture values before consumers can run.
output "identity" {
  description = "Simulated identity recorded by the local fixture."
  value       = try(terraform_data.this.output.identity, null)
}
