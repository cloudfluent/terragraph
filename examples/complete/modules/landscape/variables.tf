variable "dev" {
  description = "Dev for the local landscape fixture."
  type        = object({ stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string) })
  nullable    = false
}

variable "stg" {
  description = "Stg for the local landscape fixture."
  type        = object({ stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string) })
  nullable    = false
}

variable "prd" {
  description = "Prd for the local landscape fixture."
  type        = object({ stage = string, account_id = string, cluster_name = string, apps_vpc_id = string, data_vpc_id = string, services = list(string) })
  nullable    = false
}
