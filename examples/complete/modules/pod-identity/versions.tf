terraform {
  required_version = ">= 1.4.0, < 2.0.0"
  # An explicit local backend lets Terragraph isolate reused roots by qualified leaf name.
  backend "local" {}
}
