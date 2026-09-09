# Native import records only an ID; apply must restore fixture values before consumers can run.
output "service" {
  description = "Simulated service recorded by the local fixture."
  value       = try(terraform_data.this.output.service, null)
}
