# Native import records only an ID; apply must restore fixture values before consumers can run.
output "cache" {
  description = "Simulated cache recorded by the local fixture."
  value       = try(terraform_data.this.output.cache, null)
}
