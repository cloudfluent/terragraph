# Native import records only an ID; apply must restore fixture values before consumers can run.
output "queue" {
  description = "Simulated queue recorded by the local fixture."
  value       = try(terraform_data.this.output.queue, null)
}
