node "vpc_a" {
  source = "./stacks/vpc"
  backend_config = {
    path = ".terragraph/state/vpc_a.tfstate"
  }
}

node "vpc_b" {
  source = "./stacks/vpc"
  backend_config = {
    path = ".terragraph/state/vpc_b.tfstate"
  }
}
