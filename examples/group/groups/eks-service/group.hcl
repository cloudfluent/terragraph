group "eks-service" {
  node "cluster" {
    source = "../../modules/cluster"
  }
  node "nodegroup" {
    source = "../../modules/nodegroup"
  }

  edge {
    from = node.cluster.output.cluster_id
    to   = node.nodegroup.input.cluster_id
  }

  export {
    input "vpc_id" {
      to = node.cluster.input.vpc_id
    }
    output "cluster_id" {
      from = node.cluster.output.cluster_id
    }
  }
}
