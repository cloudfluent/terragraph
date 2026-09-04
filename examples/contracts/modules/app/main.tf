variable "vpc_id" {
  type = string
}

output "attached" {
  value = var.vpc_id
}
