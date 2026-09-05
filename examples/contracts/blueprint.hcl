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

relationship {
  between = [node.vpc, node.app]
}

producer "./modules/vpc" {
  output "vpc_id" {
    type     = "string"
    nullable = false
  }
}

consumer "./modules/app" {
  input "vpc_id" {
    type     = "string"
    nullable = false
  }
}
