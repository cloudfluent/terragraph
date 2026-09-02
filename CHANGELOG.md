# Changelog

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
