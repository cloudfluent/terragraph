producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"
    nullable  = false
    stability = "stable"
    assert {
      nonempty = true
      pattern  = "^vpc-"
    }
  }
}

consumer "./modules/app" {
  input "vpc_id" {
    type     = "string"
    nullable = false
  }
}
