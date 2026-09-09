group "account" {
  node "identity" {
    source = "../../modules/account"

  }

  node "guardrails" {
    source = "../../modules/guardrails"

  }

  edge {
    from = node.identity
    to   = node.guardrails
    input "context" {
      from = output.context
    }
  }

  export {
    input "organization" {
      to = node.identity.input.organization
    }
    input "account" {
      to = node.identity.input.account
    }
    output "context" {
      from = node.guardrails.output.context
    }
  }

  producer "../../modules/guardrails" {
    output "context" {
      type      = object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})
      nullable  = false
      sensitive = false
    }
  }

  consumer "../../modules/guardrails" {
    input "context" {
      type      = object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})
      nullable  = false
      sensitive = false
    }
  }
}
