# Every data-edge port has a declaration; business invariants live in fixture preconditions.

producer "./modules/organization" {
  output "dns_zone" {
    type      = "string"
    nullable  = false
    sensitive = false
  }
  output "organization" {
    type      = "object({tenant = string, namespace = string, region = string, region_code = string, domain = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/organization" {
  input "organization" {
    type      = "object({tenant = string, namespace = string, region = string, region_code = string, domain = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/account" {
  output "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/account" {
  input "organization" {
    type      = "object({tenant = string, namespace = string, region = string, region_code = string, domain = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "account" {
    type      = "object({account_id = string, name = string, stage = string})"
    nullable  = false
    sensitive = false
  }
}



producer "./modules/audit" {
  output "telemetry" {
    type      = "object({archive_bucket_arn = string, kms_key_arn = string, endpoint = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/audit" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/vpc" {
  output "network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/vpc" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "network_config" {
    type      = "object({purpose = string, cidr = string, availability_zones = optional(list(string)), nat_gateways = optional(number)})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/transit" {
  output "transit" {
    type      = "object({id = string, owner_account_id = string, region = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/transit" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/shared-services" {
  output "registry" {
    type      = "object({repository_url = string, owner_account_id = string})"
    nullable  = false
    sensitive = false
  }
  output "dns_zone" {
    type      = "string"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/shared-services" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "telemetry" {
    type      = "object({archive_bucket_arn = string, kms_key_arn = string, endpoint = string})"
    nullable  = false
    sensitive = false
  }
  input "domain" {
    type      = "string"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/connectivity" {
  output "connectivity" {
    type      = "object({apps_vpc_id = string, data_vpc_id = string, account_id = string, route_domain = string, transit_gateway_id = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/connectivity" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "apps_network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "data_network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "transit" {
    type      = "object({id = string, owner_account_id = string, region = string})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/eks" {
  output "cluster" {
    type      = "object({name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/eks" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "telemetry" {
    type      = "object({archive_bucket_arn = string, kms_key_arn = string, endpoint = string})"
    nullable  = false
    sensitive = false
  }
  input "cluster_config" {
    type      = "object({version = string, private_endpoint = optional(bool)})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/node-pool" {
  output "pool" {
    type      = "object({name = string, cluster_name = string, desired_size = number, capacity_type = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/node-pool" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "cluster" {
    type      = "object({name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string})"
    nullable  = false
    sensitive = false
  }
  input "pool_config" {
    type      = "object({name = string, capacity_type = string, min_size = number, desired_size = number, max_size = number})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/addons" {
  output "addons" {
    type      = "object({cluster_name = string, controllers = list(string)})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/addons" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "cluster" {
    type      = "object({name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string})"
    nullable  = false
    sensitive = false
  }
  input "telemetry" {
    type      = "object({archive_bucket_arn = string, kms_key_arn = string, endpoint = string})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/database" {
  output "database" {
    type      = "object({endpoint = string, port = number, name = string, account_id = string, vpc_id = string})"
    nullable  = false
    sensitive = false
  }
  output "credentials" {
    type      = "object({username = string, password = string})"
    nullable  = false
    sensitive = true
  }
}

consumer "./modules/database" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "data_config" {
    type      = "object({multi_az = bool, backup_retention_days = number, cache_nodes = number})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/cache" {
  output "cache" {
    type      = "object({endpoint = string, port = number, account_id = string, vpc_id = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/cache" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "network" {
    type      = "object({id = string, account_id = string, region = string, cidr = string, private_subnet_ids = list(string), private_subnet_cidrs = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "data_config" {
    type      = "object({multi_az = bool, backup_retention_days = number, cache_nodes = number})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/queue" {
  output "queue" {
    type      = "object({arn = string, url = string, account_id = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/queue" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/pod-identity" {
  output "identity" {
    type      = "object({role_arn = string, namespace = string, service_account = string, account_id = string, cluster_name = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/pod-identity" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "cluster" {
    type      = "object({name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string})"
    nullable  = false
    sensitive = false
  }
  input "queue" {
    type      = "object({arn = string, url = string, account_id = string})"
    nullable  = false
    sensitive = false
  }
  input "service_config" {
    type      = "object({name = string, image_tag = string, replicas = number})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/workload" {
  output "service" {
    type      = "object({name = string, url = string, image = string, replicas = number, account_id = string, cluster_name = string})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/workload" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "cluster" {
    type      = "object({name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string})"
    nullable  = false
    sensitive = false
  }
  input "addons" {
    type      = "object({cluster_name = string, controllers = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "connectivity" {
    type      = "object({apps_vpc_id = string, data_vpc_id = string, account_id = string, route_domain = string, transit_gateway_id = string})"
    nullable  = false
    sensitive = false
  }
  input "database" {
    type      = "object({endpoint = string, port = number, name = string, account_id = string, vpc_id = string})"
    nullable  = false
    sensitive = false
  }
  input "credentials" {
    type      = "object({username = string, password = string})"
    nullable  = false
    sensitive = true
  }
  input "cache" {
    type      = "object({endpoint = string, port = number, account_id = string, vpc_id = string})"
    nullable  = false
    sensitive = false
  }
  input "queue" {
    type      = "object({arn = string, url = string, account_id = string})"
    nullable  = false
    sensitive = false
  }
  input "identity" {
    type      = "object({role_arn = string, namespace = string, service_account = string, account_id = string, cluster_name = string})"
    nullable  = false
    sensitive = false
  }
  input "registry" {
    type      = "object({repository_url = string, owner_account_id = string})"
    nullable  = false
    sensitive = false
  }
  input "dns_zone" {
    type      = "string"
    nullable  = false
    sensitive = false
  }
  input "service_config" {
    type      = "object({name = string, image_tag = string, replicas = number})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/release" {
  output "release" {
    type      = "object({stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string)})"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/release" {
  input "context" {
    type      = "object({account_id = string, account_name = string, tenant = string, namespace = string, stage = string, region = string, region_code = string, tags = map(string)})"
    nullable  = false
    sensitive = false
  }
  input "cluster" {
    type      = "object({name = string, arn = string, endpoint = string, oidc_provider_arn = string, account_id = string, vpc_id = string, version = string})"
    nullable  = false
    sensitive = false
  }
  input "connectivity" {
    type      = "object({apps_vpc_id = string, data_vpc_id = string, account_id = string, route_domain = string, transit_gateway_id = string})"
    nullable  = false
    sensitive = false
  }
  input "checkout" {
    type      = "object({name = string, url = string, image = string, replicas = number, account_id = string, cluster_name = string})"
    nullable  = false
    sensitive = false
  }
  input "payments" {
    type      = "object({name = string, url = string, image = string, replicas = number, account_id = string, cluster_name = string})"
    nullable  = false
    sensitive = false
  }
}

producer "./modules/landscape" {
  output "environments" {
    type      = "map(object({stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string)}))"
    nullable  = false
    sensitive = false
  }
  output "summary" {
    type      = "string"
    nullable  = false
    sensitive = false
  }
}

consumer "./modules/landscape" {
  input "dev" {
    type      = "object({stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "stg" {
    type      = "object({stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string)})"
    nullable  = false
    sensitive = false
  }
  input "prd" {
    type      = "object({stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string)})"
    nullable  = false
    sensitive = false
  }
}
