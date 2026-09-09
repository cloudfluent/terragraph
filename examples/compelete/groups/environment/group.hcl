group "environment" {
  use "account" {
    as     = "account"
    source = "../account"

  }

  use "network" {
    as     = "network"
    source = "../network"

  }

  use "eks" {
    as     = "platform"
    source = "../eks"
    env    = { TF_INPUT = "0" }
  }

  use "data" {
    as     = "data"
    source = "../data"

  }

  use "service" {
    as     = "checkout"
    source = "../service"

  }

  use "service" {
    as     = "payments"
    source = "../service"

  }

  node "release" {
    source = "../../modules/release"

  }

  edge {
    from = use.account
    to   = use.network
    input "context" {
      from = output.context
    }
  }

  edge {
    from = use.account
    to   = use.platform
    input "context" {
      from = output.context
    }
  }

  edge {
    from = use.account
    to   = use.data
    input "context" {
      from = output.context
    }
  }

  edge {
    from = use.account
    to   = use.checkout
    input "context" {
      from = output.context
    }
  }

  edge {
    from = use.account
    to   = use.payments
    input "context" {
      from = output.context
    }
  }

  edge {
    from = use.account
    to   = node.release
    input "context" {
      from = output.context
    }
  }

  edge {
    from = use.network
    to   = use.platform
    input "network" {
      from = output.apps_network
    }
  }

  edge {
    from = use.network
    to   = use.data
    input "network" {
      from = output.data_network
    }
  }

  # Group ordering waits for TGW routing even though EKS only consumes the apps VPC.

  edge {
    from = use.network
    to   = use.platform
  }

  edge {
    from = use.platform
    to   = use.checkout
    input "cluster" {
      from = output.cluster
    }
    input "addons" {
      from = output.addons
    }
  }

  edge {
    from = use.network
    to   = use.checkout
    input "connectivity" {
      from = output.connectivity
    }
  }

  edge {
    from = use.data
    to   = use.checkout
    input "database" {
      from = output.database
    }
    input "credentials" {
      from = output.credentials
    }
    input "cache" {
      from = output.cache
    }
    input "queue" {
      from = output.queue
    }
  }

  edge {
    from = use.platform
    to   = use.payments
    input "cluster" {
      from = output.cluster
    }
    input "addons" {
      from = output.addons
    }
  }

  edge {
    from = use.network
    to   = use.payments
    input "connectivity" {
      from = output.connectivity
    }
  }

  edge {
    from = use.data
    to   = use.payments
    input "database" {
      from = output.database
    }
    input "credentials" {
      from = output.credentials
    }
    input "cache" {
      from = output.cache
    }
    input "queue" {
      from = output.queue
    }
  }

  edge {
    from = use.platform
    to   = node.release
    input "cluster" {
      from = output.cluster
    }
  }

  edge {
    from = use.network
    to   = node.release
    input "connectivity" {
      from = output.connectivity
    }
  }

  edge {
    from = use.checkout
    to   = node.release
    input "checkout" {
      from = output.service
    }
  }

  edge {
    from = use.payments
    to   = node.release
    input "payments" {
      from = output.service
    }
  }

  export {
    input "organization" {
      to = use.account.input.organization
    }
    input "account" {
      to = use.account.input.account
    }
    input "apps_config" {
      to = use.network.input.apps_config
    }
    input "data_network_config" {
      to = use.network.input.data_config
    }
    input "transit" {
      to = use.network.input.transit
    }
    input "cluster_config" {
      to = use.platform.input.cluster_config
    }
    input "pool_config" {
      to = use.platform.input.pool_config
    }
    input "data_config" {
      to = use.data.input.data_config
    }
    input "telemetry" {
      to = use.platform.input.telemetry
    }
    input "registry" {
      to = [use.checkout.input.registry, use.payments.input.registry]
    }
    input "dns_zone" {
      to = [use.checkout.input.dns_zone, use.payments.input.dns_zone]
    }
    input "checkout_config" {
      to = use.checkout.input.service_config
    }
    input "payments_config" {
      to = use.payments.input.service_config
    }
    output "release" {
      from = node.release.output.release
    }
  }
}
