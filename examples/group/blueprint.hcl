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

use "eks-service" {
  as     = "payments"
  source = "./groups/eks-service"
  vars = {
    cluster_name = "payments"
  }
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.checkout.input.vpc_id
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.payments.input.vpc_id
}
