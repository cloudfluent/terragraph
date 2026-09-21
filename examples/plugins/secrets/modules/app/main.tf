variable "db_password" {
  type      = string
  sensitive = true # resolver values arrive sensitive; the module must declare it so Terraform protects presentation too
}
