variable "vpc_id" {
  type        = string
  description = "Wired in by terragraph from the group's export input"
}

variable "cluster_name" {
  type        = string
  description = "Instance-specific name supplied via use.vars"
}
