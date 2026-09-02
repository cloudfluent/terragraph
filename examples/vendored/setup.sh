#!/usr/bin/env bash
# blueprint.hcl, vendor/, and vendor.yaml are already committed in this example, you don't need to run this to `validate`/`graph`/`apply` it. This script only exists to *regenerate* them from scratch, to watch `terragraph vendor` actually fetch something rather than just look at its output: it builds a throwaway local git repo standing in for "some third-party module on GitHub" and regenerates blueprint.hcl to point at it, so the regeneration stays fully offline and reproducible, without depending on any real external repository staying available. A real project vendors a real https://... URL directly in a hand-written blueprint.hcl; this script (and the git::file://... address it produces) exists purely to make *regenerating this example* self-contained. See internal/vendor/git_integration_test.go for the same technique used as an automated test. Running it will overwrite the committed blueprint.hcl with one pointing at *your* machine's local path, re-vendor (`terragraph vendor --force`) after, since the old vendor/ still matches the old (committed) source.
set -euo pipefail
cd "$(dirname "$0")"

rm -rf upstream
mkdir -p upstream

cat > upstream/main.tf << 'EOF'
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

resource "random_id" "vpc" {
  byte_length = 4
}
EOF

cat > upstream/outputs.tf << 'EOF'
output "vpc_id" {
  value = "vpc-${random_id.vpc.hex}"
}
EOF

cat > upstream/README.md << 'EOF'
Pretend third-party module. Deliberately not a .tf file, so `exclude = ["*.md"]` (see the README section on vendoring) has something real to prune.
EOF

git -C upstream init -q -b main
git -C upstream -c user.name=terragraph-example -c user.email=example@terragraph.local add .
git -C upstream -c user.name=terragraph-example -c user.email=example@terragraph.local commit -q -m "initial"
git -C upstream tag v1.0.0

cat > blueprint.hcl << EOF
node "vpc" {
  source = "git::file://$(pwd)/upstream?ref=v1.0.0"
}
EOF

echo "ready, upstream fixture repo (re)created and blueprint.hcl regenerated to point at it."
echo "next: go run ../../cmd/terragraph vendor --force && go run ../../cmd/terragraph apply --auto-approve"
