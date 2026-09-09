# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    organization = var.organization
    dns_zone     = var.organization.domain
  }

}
