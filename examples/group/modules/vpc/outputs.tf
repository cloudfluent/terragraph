output "vpc_id" {
  value = "vpc-${random_id.vpc.hex}"
}
