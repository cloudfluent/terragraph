node "vpc" {
  source = "./stacks/vpc"
}

node "eks" {
  source = "./stacks/eks"
}

node "eks2" {
  source = "./stacks/eks2"
}

edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}

edge {
  from = node.vpc.output.vpc_id
  to   = node.eks2.input.vpc_id
}
