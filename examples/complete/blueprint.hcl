# Load this directory, not this file alone: contracts, environments, and settings are siblings.
node "organization" {
  source = "./modules/organization"
  vars = {
    organization = {
      tenant      = "acme"
      namespace   = "commerce-platform"
      region      = "ap-northeast-2"
      region_code = "ane2"
      domain      = "commerce.example.invalid"
      tags        = { Owner = "platform-team", CostCenter = "commerce" }
    }
  }
}
use "account" {
  as     = "security"
  source = "./groups/account"
  vars   = { account = { account_id = "111111111111", name = "security", stage = "common" } }
  env    = { AWS_REGION = "ap-northeast-2", AWS_EC2_METADATA_DISABLED = "true" }
}
use "account" {
  as     = "network"
  source = "./groups/account"
  vars   = { account = { account_id = "222222222222", name = "network", stage = "common" } }
  env    = { AWS_REGION = "ap-northeast-2", AWS_EC2_METADATA_DISABLED = "true" }
}
use "account" {
  as     = "shared"
  source = "./groups/account"
  vars   = { account = { account_id = "333333333333", name = "shared", stage = "common" } }
  env    = { AWS_REGION = "ap-northeast-2", AWS_EC2_METADATA_DISABLED = "true" }
}
node "audit" { source = "./modules/audit" }
node "hub" {
  source = "./modules/vpc"
  vars   = { network_config = { purpose = "hub", cidr = "10.0.0.0/16" } }
}
node "transit" { source = "./modules/transit" }
node "shared_services" {
  source = "./modules/shared-services"
}
node "landscape" {
  source = "./modules/landscape"
  # These are local fixtures; all permits teardown of this disposable catalog.
  approve = "all"
}
edge {
  from = node.organization.output.organization
  to   = use.security.input.organization
}

edge {
  from = node.organization
  to   = use.network
  input "organization" {
    from = output.organization
  }
}

edge {
  from = node.organization
  to   = use.shared
  input "organization" {
    from = output.organization
  }
}

edge {
  from = use.security
  to   = node.audit
  input "context" {
    from = output.context
  }
}

edge {
  from = use.network
  to   = node.hub
  input "context" {
    from = output.context
  }
}

edge {
  from = use.network
  to   = node.transit
  input "context" {
    from = output.context
  }
}

edge {
  from = node.hub
  to   = node.transit
  input "network" {
    from = output.network
  }
}

edge {
  from = use.shared
  to   = node.shared_services
  input "context" {
    from = output.context
  }
}

edge {
  from = node.audit
  to   = node.shared_services
  input "telemetry" {
    from = output.telemetry
  }
}

edge {
  from = node.organization.output.dns_zone
  to   = node.shared_services.input.domain
}
