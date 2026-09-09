group "network" {
  node "apps" {
    source = "../../modules/vpc"

  }

  node "data" {
    source = "../../modules/vpc"

  }

  node "connectivity" {
    source = "../../modules/connectivity"

  }

  edge {
    from = node.apps
    to   = node.connectivity
    input "apps_network" {
      from = output.network
    }
  }

  edge {
    from = node.data
    to   = node.connectivity
    input "data_network" {
      from = output.network
    }
  }

  export {
    input "context" {
      to = [node.apps.input.context, node.data.input.context, node.connectivity.input.context]
    }
    input "apps_config" {
      to = node.apps.input.network_config
    }
    input "data_config" {
      to = node.data.input.network_config
    }
    input "transit" {
      to = node.connectivity.input.transit
    }
    output "apps_network" {
      from = node.apps.output.network
    }
    output "data_network" {
      from = node.data.output.network
    }
    output "connectivity" {
      from = node.connectivity.output.connectivity
    }
  }
}
