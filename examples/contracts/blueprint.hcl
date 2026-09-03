node "vpc" {
  source = "./modules/vpc"
}

node "app" {
  source = "./modules/app"
}

edge {
  from = node.vpc.output.vpc_id
  to   = node.app.input.vpc_id
}
