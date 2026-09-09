# Native import records only an ID; apply must restore fixture values before consumers can run.
output "telemetry" {
  description = "Simulated telemetry recorded by the local fixture."
  value       = try(terraform_data.this.output.telemetry, null)
}
