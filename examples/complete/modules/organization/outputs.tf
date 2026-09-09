# Native import records only an ID; apply must restore fixture values before consumers can run.
output "organization" {
  description = "Simulated organization recorded by the local fixture."
  value       = try(terraform_data.this.output.organization, null)
}

output "dns_zone" {
  description = "Reserved DNS suffix for simulated application endpoints."
  value       = try(terraform_data.this.output.dns_zone, null)
}
