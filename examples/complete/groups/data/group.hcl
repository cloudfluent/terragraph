group "data" {
  node "database" {
    source = "../../modules/database"

  }

  node "cache" {
    source = "../../modules/cache"

  }

  node "queue" {
    source = "../../modules/queue"

  }

  export {
    input "context" {
      to = [node.database.input.context, node.cache.input.context, node.queue.input.context]
    }
    input "network" {
      to = [node.database.input.network, node.cache.input.network]
    }
    input "data_config" {
      to = [node.database.input.data_config, node.cache.input.data_config]
    }
    output "database" {
      from = node.database.output.database
    }
    output "credentials" {
      from = node.database.output.credentials
    }
    output "cache" {
      from = node.cache.output.cache
    }
    output "queue" {
      from = node.queue.output.queue
    }
  }
}
