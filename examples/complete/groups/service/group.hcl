group "service" {
  node "identity" {
    source = "../../modules/pod-identity"

  }

  node "deployment" {
    source = "../../modules/workload"

  }

  edge {
    from = node.identity
    to   = node.deployment
    input "identity" {
      from = output.identity
    }
  }

  export {
    input "context" {
      to = [node.identity.input.context, node.deployment.input.context]
    }
    input "cluster" {
      to = [node.identity.input.cluster, node.deployment.input.cluster]
    }
    input "addons" {
      to = node.deployment.input.addons
    }
    input "connectivity" {
      to = node.deployment.input.connectivity
    }
    input "database" {
      to = node.deployment.input.database
    }
    input "credentials" {
      to = node.deployment.input.credentials
    }
    input "cache" {
      to = node.deployment.input.cache
    }
    input "queue" {
      to = [node.identity.input.queue, node.deployment.input.queue]
    }
    input "registry" {
      to = node.deployment.input.registry
    }
    input "dns_zone" {
      to = node.deployment.input.dns_zone
    }
    input "service_config" {
      to = [node.identity.input.service_config, node.deployment.input.service_config]
    }
    output "service" {
      from = node.deployment.output.service
    }
  }
}
