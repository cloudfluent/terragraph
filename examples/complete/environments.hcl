use "environment" {
  as     = "dev"
  source = "./groups/environment"
  env    = { AWS_REGION = "ap-northeast-2", AWS_EC2_METADATA_DISABLED = "true", TF_INPUT = "0" }
  vars = {
    account             = { account_id = "444444444444", name = "workloads-dev", stage = "dev" }
    apps_config         = { purpose = "apps", cidr = "10.16.0.0/16", nat_gateways = 1 }
    data_network_config = { purpose = "data", cidr = "10.17.0.0/16" }
    cluster_config      = { version = "1.34" }
    pool_config         = { name = "workloads", capacity_type = "SPOT", min_size = 2, desired_size = 2, max_size = 6 }
    data_config         = { multi_az = false, backup_retention_days = 1, cache_nodes = 1 }
    checkout_config     = { name = "checkout", image_tag = "v1.0.0", replicas = 1 }
    payments_config     = { name = "payments", image_tag = "v1.0.0", replicas = 1 }
  }
}

edge {
  from = node.organization
  to   = use.dev
  input "organization" {
    from = output.organization
  }
}

edge {
  from = node.transit
  to   = use.dev
  input "transit" {
    from = output.transit
  }
}

edge {
  from = node.audit
  to   = use.dev
  input "telemetry" {
    from = output.telemetry
  }
}

edge {
  from = node.shared_services
  to   = use.dev
  input "registry" {
    from = output.registry
  }
  input "dns_zone" {
    from = output.dns_zone
  }
}

edge {
  from = use.dev
  to   = node.landscape
  input "dev" {
    from = output.release
  }
}

use "environment" {
  as     = "stg"
  source = "./groups/environment"
  env    = { AWS_REGION = "ap-northeast-2", AWS_EC2_METADATA_DISABLED = "true", TF_INPUT = "0" }
  vars = {
    account             = { account_id = "555555555555", name = "workloads-stg", stage = "stg" }
    apps_config         = { purpose = "apps", cidr = "10.32.0.0/16", nat_gateways = 1 }
    data_network_config = { purpose = "data", cidr = "10.33.0.0/16" }
    cluster_config      = { version = "1.34" }
    pool_config         = { name = "workloads", capacity_type = "ON_DEMAND", min_size = 2, desired_size = 2, max_size = 6 }
    data_config         = { multi_az = true, backup_retention_days = 7, cache_nodes = 2 }
    checkout_config     = { name = "checkout", image_tag = "v1.0.0", replicas = 2 }
    payments_config     = { name = "payments", image_tag = "v1.0.0", replicas = 2 }
  }
}

edge {
  from = node.organization
  to   = use.stg
  input "organization" {
    from = output.organization
  }
}

edge {
  from = node.transit
  to   = use.stg
  input "transit" {
    from = output.transit
  }
}

edge {
  from = node.audit
  to   = use.stg
  input "telemetry" {
    from = output.telemetry
  }
}

edge {
  from = node.shared_services
  to   = use.stg
  input "registry" {
    from = output.registry
  }
  input "dns_zone" {
    from = output.dns_zone
  }
}

edge {
  from = use.stg
  to   = node.landscape
  input "stg" {
    from = output.release
  }
}

use "environment" {
  as = "prd"
  # A standing production policy cannot be widened by --approve or --auto-approve.
  approve = "safe"
  source  = "./groups/environment"
  env     = { AWS_REGION = "ap-northeast-2", AWS_EC2_METADATA_DISABLED = "true", TF_INPUT = "0" }
  vars = {
    account             = { account_id = "666666666666", name = "workloads-prd", stage = "prd" }
    apps_config         = { purpose = "apps", cidr = "10.48.0.0/16", nat_gateways = 3 }
    data_network_config = { purpose = "data", cidr = "10.49.0.0/16" }
    cluster_config      = { version = "1.34" }
    pool_config         = { name = "workloads", capacity_type = "ON_DEMAND", min_size = 2, desired_size = 4, max_size = 12 }
    data_config         = { multi_az = true, backup_retention_days = 35, cache_nodes = 3 }
    checkout_config     = { name = "checkout", image_tag = "v1.0.0", replicas = 3 }
    payments_config     = { name = "payments", image_tag = "v1.0.0", replicas = 3 }
  }
}

edge {
  from = node.organization
  to   = use.prd
  input "organization" {
    from = output.organization
  }
}

edge {
  from = node.transit
  to   = use.prd
  input "transit" {
    from = output.transit
  }
}

edge {
  from = node.audit
  to   = use.prd
  input "telemetry" {
    from = output.telemetry
  }
}

edge {
  from = node.shared_services
  to   = use.prd
  input "registry" {
    from = output.registry
  }
  input "dns_zone" {
    from = output.dns_zone
  }
}

edge {
  from = use.prd
  to   = node.landscape
  input "prd" {
    from = output.release
  }
}
