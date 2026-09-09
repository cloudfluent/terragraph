# Native import records only an ID; apply must restore fixture values before consumers can run.
output "cluster" {
  description = "Simulated cluster recorded by the local fixture."
  value       = try(terraform_data.this.output.cluster, null)
}
