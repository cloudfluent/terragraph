# Native import records only an ID; apply must restore fixture values before consumers can run.
output "registry" {
  description = "Simulated registry recorded by the local fixture."
  value       = try(terraform_data.this.output.registry, null)
}

output "dns_zone" {
  description = "Simulated dns zone recorded by the local fixture."
  value       = try(terraform_data.this.output.dns_zone, null)
}
