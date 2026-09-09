# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    addons = {
      cluster_name = var.cluster.name
      controllers  = ["vpc-cni", "coredns", "kube-proxy", "ebs-csi", "pod-identity-agent", "aws-load-balancer-controller", "external-dns", "external-secrets", "opentelemetry"]
    }
    telemetry_endpoint = var.telemetry.endpoint
    tags               = var.context.tags
  }

  lifecycle {
    precondition {
      condition     = var.context.account_id == var.cluster.account_id
      error_message = "Cluster belongs to another account; connect this environment's cluster export."
    }
  }
}
