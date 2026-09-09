group "eks" {
  node "cluster" {
    source = "../../modules/eks"

  }

  node "system" {
    env    = { TF_INPUT = "false" }
    source = "../../modules/node-pool"
    vars   = { pool_config = { name = "system", capacity_type = "ON_DEMAND", min_size = 2, desired_size = 2, max_size = 3 } }
  }

  node "workloads" {
    source = "../../modules/node-pool"

  }

  node "addons" {
    source = "../../modules/addons"

  }

  edge {
    from = node.cluster
    to   = node.system
    input "cluster" {
      from = output.cluster
    }
  }

  edge {
    from = node.cluster
    to   = node.workloads
    input "cluster" {
      from = output.cluster
    }
  }

  edge {
    from = node.cluster
    to   = node.addons
    input "cluster" {
      from = output.cluster
    }
  }

  # Ordering edges wait for node pools without inventing an input on the add-on module.

  edge {
    from = node.system
    to   = node.addons
  }

  edge {
    from = node.workloads
    to   = node.addons
  }

  export {
    input "context" {
      to = [node.cluster.input.context, node.system.input.context, node.workloads.input.context, node.addons.input.context]
    }
    input "network" {
      to = node.cluster.input.network
    }
    input "telemetry" {
      to = [node.cluster.input.telemetry, node.addons.input.telemetry]
    }
    input "cluster_config" {
      to = node.cluster.input.cluster_config
    }
    input "pool_config" {
      to = node.workloads.input.pool_config
    }
    output "cluster" {
      from = node.cluster.output.cluster
    }
    output "addons" {
      from = node.addons.output.addons
    }
  }
}
