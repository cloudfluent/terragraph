# Changelog

## [0.2.1](https://github.com/cloudfluent/terragraph/compare/v0.2.0...v0.2.1) (2026-09-03)


### Features

* add optional S3 graph remote lock ([#46](https://github.com/cloudfluent/terragraph/issues/46)) ([a235d5b](https://github.com/cloudfluent/terragraph/commit/a235d5b501d4ac30bfd78e4333200c18e7711397))
* allow literal vars on use to fill group export inputs ([#42](https://github.com/cloudfluent/terragraph/issues/42)) ([8c1b159](https://github.com/cloudfluent/terragraph/commit/8c1b1590933d23f7a32cccfaf0d51dac0ff07118))
* isolate terraform state for shared module sources ([#43](https://github.com/cloudfluent/terragraph/issues/43)) ([9e39039](https://github.com/cloudfluent/terragraph/commit/9e39039b0432d7120ed9ee703aaf6644394d9f81))


### Bug Fixes

* let destroy be approved too, instead of failing at its own prompt ([#32](https://github.com/cloudfluent/terragraph/issues/32)) ([a555a92](https://github.com/cloudfluent/terragraph/commit/a555a928bc167c4425aa3d35bc3d5afd1007e926))
* offer approve in editor completion, and stop the two schemas drifting ([#41](https://github.com/cloudfluent/terragraph/issues/41)) ([bff0338](https://github.com/cloudfluent/terragraph/commit/bff033814621d27390ad19a9e6bf543db9ac54bc))
* serialize concurrent terragraph processes with a blueprint lock ([#31](https://github.com/cloudfluent/terragraph/issues/31)) ([f3295e5](https://github.com/cloudfluent/terragraph/commit/f3295e5d2e05303cb107dcbf99c929d97413b9d8))


### Documentation

* add agent conventions and PR body requirements ([911bf74](https://github.com/cloudfluent/terragraph/commit/911bf74c8836c890f19adfeb88c21a22d1569b2e))
* contrast terragraph with terraform_remote_state in the README ([#44](https://github.com/cloudfluent/terragraph/issues/44)) ([42cefce](https://github.com/cloudfluent/terragraph/commit/42cefcefeb2e0f1738b258c9655bed211b9c10b5))
* drop em-dashes from the README opening ([#45](https://github.com/cloudfluent/terragraph/issues/45)) ([009dc40](https://github.com/cloudfluent/terragraph/commit/009dc401c42ac417bddbecbe439a34394fa519e6))

## [0.2.0](https://github.com/cloudfluent/terragraph/compare/v0.1.5...v0.2.0) (2026-09-02)


### ⚠ BREAKING CHANGES

* make applying a granted permission and let Terraform decide what needs applying ([#30](https://github.com/cloudfluent/terragraph/issues/30))

### Features

* make applying a granted permission and let Terraform decide what needs applying ([#30](https://github.com/cloudfluent/terragraph/issues/30)) ([df69e08](https://github.com/cloudfluent/terragraph/commit/df69e085ebc6e0b732d1afae8f848056f7b12862))


### Bug Fixes

* verify incremental cache hits with refreshed plans ([#22](https://github.com/cloudfluent/terragraph/issues/22)) ([5aba5c6](https://github.com/cloudfluent/terragraph/commit/5aba5c643451fa3f3725e7176eaf8d8a7850b278))

## [0.1.5](https://github.com/cloudfluent/terragraph/compare/v0.1.4...v0.1.5) (2026-09-02)


### Features

* allow multiple input mappings on one edge via nested input blocks ([#23](https://github.com/cloudfluent/terragraph/issues/23)) ([ac1035e](https://github.com/cloudfluent/terragraph/commit/ac1035e100cf6ac27dfeba233f225fd3f4981527))
* reject multiple data edges targeting the same input ([#19](https://github.com/cloudfluent/terragraph/issues/19)) ([8c92632](https://github.com/cloudfluent/terragraph/commit/8c92632b927da0308f42e4d93ae6033189a44c1b))

## [0.1.4](https://github.com/cloudfluent/terragraph/compare/v0.1.3...v0.1.4) (2026-09-02)


### Bug Fixes

* **release:** prevent duplicate publishing ([#10](https://github.com/cloudfluent/terragraph/issues/10)) ([3fef8cc](https://github.com/cloudfluent/terragraph/commit/3fef8cc7ba0b2e83b0545080feec6e0d169143d8))

## [0.1.3](https://github.com/cloudfluent/terragraph/compare/v0.1.2...v0.1.3) (2026-09-02)


### Features

* add per-node runtime, env, and tfvars location ([203d35e](https://github.com/cloudfluent/terragraph/commit/203d35e8b031742fa08e1addbc0756011c933953))
* let --blueprint merge a directory of .hcl files ([#6](https://github.com/cloudfluent/terragraph/issues/6)) ([8bfc9e5](https://github.com/cloudfluent/terragraph/commit/8bfc9e5aff01f7ee88fcb0721c8a74ab88c51faf))
* **vscode:** add Blueprint language intelligence ([#8](https://github.com/cloudfluent/terragraph/issues/8)) ([eeaaac1](https://github.com/cloudfluent/terragraph/commit/eeaaac1bebffd4108ec445578effa24154bdba0f))


### Documentation

* add release version badge to README ([c938c23](https://github.com/cloudfluent/terragraph/commit/c938c23539214810496a49134d92e03b66b6946f))

## [0.1.2](https://github.com/cloudfluent/terragraph/compare/v0.1.1...v0.1.2) (2026-09-02)


### Bug Fixes

* strip quarantine in a cask preflight, not postflight ([3f32b32](https://github.com/cloudfluent/terragraph/commit/3f32b32b355f366b3e1a4f8dd13c6e588106a52f))


### Documentation

* remove dangling doc-sync file reference from CONTRIBUTING ([a010b94](https://github.com/cloudfluent/terragraph/commit/a010b941a34dca95a64dd0a7eb57fbdf12c33126))

## [0.1.1](https://github.com/cloudfluent/terragraph/compare/v0.1.0...v0.1.1) (2026-09-02)


### Bug Fixes

* ad-hoc sign darwin binaries so Apple Silicon doesn't SIGKILL them ([4ce06b7](https://github.com/cloudfluent/terragraph/commit/4ce06b7dc65b73060a376ea702402989c7aa289b))
* allow manually re-triggering release-please ([47055f8](https://github.com/cloudfluent/terragraph/commit/47055f8c199cfa5571aaff2907c8e9bf34c33604))
* stop skipping the GitHub release in release-please-config.json ([14b663d](https://github.com/cloudfluent/terragraph/commit/14b663d2bb64a3e89ac257ed6474b259597a2a04))
* use a fine-grained PAT for release-please's own API calls ([911b675](https://github.com/cloudfluent/terragraph/commit/911b675bc37e55f91e3f7ccf8c68b26650a4f819))


### Documentation

* document brew install in README ([137082c](https://github.com/cloudfluent/terragraph/commit/137082c0d9bcbc7e592ea000adcffe15dcd780b2))

## 0.1.0 (2026-09-02)


### Features

* add contributor workflow governance ([99607e7](https://github.com/cloudfluent/terragraph/commit/99607e7680fd58286bc27d101d5586027814f65d))
* initial terragraph graph-based Terraform orchestration engine ([b065109](https://github.com/cloudfluent/terragraph/commit/b065109d430f4928de35e000b3f4bb430e3fd269))


### Bug Fixes

* pin the first release-please version to 0.1.0 ([dabfa52](https://github.com/cloudfluent/terragraph/commit/dabfa5214a0b6160a34453d9eb41a0596ecfecd7))
* re-run the PR-title check on synchronize too ([230e5d3](https://github.com/cloudfluent/terragraph/commit/230e5d3ea0765e0b76a61c491ad2796fce2e28be))
* replace deprecated brews with homebrew_casks ([8c8dfff](https://github.com/cloudfluent/terragraph/commit/8c8dfffedbf0e9bfa8bb9d85463838d1ba0ac774))
* use the real amannn/action-semantic-pull-request action ([b9280eb](https://github.com/cloudfluent/terragraph/commit/b9280ebcb295e17e4a8aa978153cd15ebd6d597f))
