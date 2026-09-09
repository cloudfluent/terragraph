# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    environments = { dev = var.dev, stg = var.stg, prd = var.prd }
    summary      = "3 environments, 6 application endpoints; all infrastructure is simulated locally"

  }

  lifecycle {
    precondition {
      condition     = length(distinct([var.dev.account_id, var.stg.account_id, var.prd.account_id])) == 3
      error_message = "Use a separate account for dev, stg, and prd; correct the environment account IDs."
    }
  }
}
