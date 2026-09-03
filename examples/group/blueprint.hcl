node "vpc" {
  source = "./modules/vpc"
}

use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
  vars = {
    cluster_name = "checkout"
  }
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.checkout.input.vpc_id
}
