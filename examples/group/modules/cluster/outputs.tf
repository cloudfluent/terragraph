output "cluster_id" {
  value = "cluster-${random_id.cluster.hex}"
}
