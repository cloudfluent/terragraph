# Changelog

## [0.2.7](https://github.com/cloudfluent/terragraph/compare/v0.2.6...v0.2.7) (2026-09-09)


### Features

* add executable plugins with HCL functions and SDK logging ([#117](https://github.com/cloudfluent/terragraph/issues/117)) ([fa62784](https://github.com/cloudfluent/terragraph/commit/fa627840f32e91fcf10e7ed500b8237ca363db45))
* add plugin lifecycle core and first-party debug observer ([#125](https://github.com/cloudfluent/terragraph/issues/125)) ([d4cddfc](https://github.com/cloudfluent/terragraph/commit/d4cddfc478b304644173be1a54865398cc78d98a))

## [0.2.6](https://github.com/cloudfluent/terragraph/compare/v0.2.5...v0.2.6) (2026-09-09)


### Features

* add optional S3 state address generation ([#114](https://github.com/cloudfluent/terragraph/issues/114)) ([1ae39a7](https://github.com/cloudfluent/terragraph/commit/1ae39a7b44801caf14d5d8738eed1477d921790a))
* enforce runtime contracts and accept native type expressions ([#111](https://github.com/cloudfluent/terragraph/issues/111)) ([acc8250](https://github.com/cloudfluent/terragraph/commit/acc8250c8147245ee63e960713b18d325cb1a78c))
* improve CLI results and diagnostics for agents ([#110](https://github.com/cloudfluent/terragraph/issues/110)) ([83ed519](https://github.com/cloudfluent/terragraph/commit/83ed51909c8402f5099828516a97249c33537d05))


### Documentation

* add complete multi-account AWS example ([#107](https://github.com/cloudfluent/terragraph/issues/107)) ([904a154](https://github.com/cloudfluent/terragraph/commit/904a154516bcfab05a09a28efc4527747f669720))
* correct complete example spelling ([#109](https://github.com/cloudfluent/terragraph/issues/109)) ([cb6dbd0](https://github.com/cloudfluent/terragraph/commit/cb6dbd073952e9321d6735a7a43d11dddc2c8588))
* explain DRY backend configuration in guides and examples ([#116](https://github.com/cloudfluent/terragraph/issues/116)) ([b65a3cc](https://github.com/cloudfluent/terragraph/commit/b65a3cc84effcb6030b825eeb950e6a3154e3c92))
* highlight AI agent friendliness in README ([#115](https://github.com/cloudfluent/terragraph/issues/115)) ([54a4683](https://github.com/cloudfluent/terragraph/commit/54a4683fe52c27b0d9c311eedc6e9cd9a0f7d42f))
* use native contract types in the complete example ([#112](https://github.com/cloudfluent/terragraph/issues/112)) ([6cb81b9](https://github.com/cloudfluent/terragraph/commit/6cb81b98f42ca098e51b053d6a746a583f738161))

## [0.2.5](https://github.com/cloudfluent/terragraph/compare/v0.2.4...v0.2.5) (2026-09-09)


### Features

* add explicit execution scope selection ([bbaf7de](https://github.com/cloudfluent/terragraph/commit/bbaf7dee092cf4d3960fbc6118e8235e328741cd))

## [0.2.4](https://github.com/cloudfluent/terragraph/compare/v0.2.3...v0.2.4) (2026-09-08)


### Features

* add protected execution records and history commands ([#101](https://github.com/cloudfluent/terragraph/issues/101)) ([dac4f70](https://github.com/cloudfluent/terragraph/commit/dac4f70bfedbf7572a6661bc06425646880c3bcf))
* add revision-checked execution artifact storage ([#100](https://github.com/cloudfluent/terragraph/issues/100)) ([4e20c9a](https://github.com/cloudfluent/terragraph/commit/4e20c9a8e1a6c54b46fef90e1d81c16eaab60b8d))
* add scoped native node operations and recovery backups ([#104](https://github.com/cloudfluent/terragraph/issues/104)) ([be6c675](https://github.com/cloudfluent/terragraph/commit/be6c6759661d57bd7bf5551fc538c0d7ac810261))
* load blueprint directories by default ([#97](https://github.com/cloudfluent/terragraph/issues/97)) ([4f378d6](https://github.com/cloudfluent/terragraph/commit/4f378d6d9412743be8aae9b4042739d1417194f2))
* record graph mutations and recover without blind replay ([#102](https://github.com/cloudfluent/terragraph/issues/102)) ([c4c7f86](https://github.com/cloudfluent/terragraph/commit/c4c7f86dbb35b7ce51b54c2544274b2de702edee))
* retain and apply reviewed graph plan frontiers ([#103](https://github.com/cloudfluent/terragraph/issues/103)) ([a4f3b60](https://github.com/cloudfluent/terragraph/commit/a4f3b602dd724dc973a120fd4ee5588b8a98d41b))


### Code Refactoring

* share prepared node plan application ([#98](https://github.com/cloudfluent/terragraph/issues/98)) ([02c5cb4](https://github.com/cloudfluent/terragraph/commit/02c5cb49529025092433c5fe61f52b80a7b9ce5f))

## [0.2.3](https://github.com/cloudfluent/terragraph/compare/v0.2.2...v0.2.3) (2026-09-08)


### Features

* add isolated node output observation ([#90](https://github.com/cloudfluent/terragraph/issues/90)) ([b502b92](https://github.com/cloudfluent/terragraph/commit/b502b92d82114e2551aef15ea2c77b9a0ad904e9))
* expose safe node state status ([#91](https://github.com/cloudfluent/terragraph/issues/91)) ([dedac03](https://github.com/cloudfluent/terragraph/commit/dedac037753dc3194c1757d1b9a412dbbc0b74bd))
* report structured plan evidence and policy assessment ([#92](https://github.com/cloudfluent/terragraph/issues/92)) ([ea87b14](https://github.com/cloudfluent/terragraph/commit/ea87b146a77143a3baefdedd4c645eb8c505334a))

## [0.2.2](https://github.com/cloudfluent/terragraph/compare/v0.2.1...v0.2.2) (2026-09-08)


### Features

* add --output json to plan, apply, and destroy ([#64](https://github.com/cloudfluent/terragraph/issues/64)) ([1f02aca](https://github.com/cloudfluent/terragraph/commit/1f02aca4ae6db25384deb8358e6167be96a16f2a))
* add two-sided contracts keyed by module source ([1e5f342](https://github.com/cloudfluent/terragraph/commit/1e5f3420f53e7c0a9b0eb4aecc4eabb4aeb9a4d0)), closes [#49](https://github.com/cloudfluent/terragraph/issues/49)
* opt-in output snapshots with last-resort resolution ([#65](https://github.com/cloudfluent/terragraph/issues/65)) ([2b8b746](https://github.com/cloudfluent/terragraph/commit/2b8b746cc572b91e2dcaa28ca06f95c940bbf31b))
* terragraph force-unlock releases a leftover graph lock (--yes required) ([5cee803](https://github.com/cloudfluent/terragraph/commit/5cee80340be14a20836a967a1d0312cd2cc7f3d5))


### Bug Fixes

* a node's explicit backend path no longer claims a same-named orphan ([7872629](https://github.com/cloudfluent/terragraph/commit/7872629c2cdc5118e15bb32cc16b3093e4b1169c))
* accept Terraform-compatible input values ([#74](https://github.com/cloudfluent/terragraph/issues/74)) ([8274208](https://github.com/cloudfluent/terragraph/commit/8274208156c4aa5b1dda46257304717e52d54dd9))
* align module inspection and graph composition checks ([#76](https://github.com/cloudfluent/terragraph/issues/76)) ([fde76b5](https://github.com/cloudfluent/terragraph/commit/fde76b5b23ee07efd27bbbf67ddada4e73b71dd1))
* align runtime inspection and verify OpenTofu file support ([#82](https://github.com/cloudfluent/terragraph/issues/82)) ([493fb8e](https://github.com/cloudfluent/terragraph/commit/493fb8e38848a7fa12c8a417aa66cee2552716a7))
* C007 allows a contract narrower than the module it describes ([9d4545b](https://github.com/cloudfluent/terragraph/commit/9d4545bfabb77dd9020293db7051eede1608ba77))
* contract diagnostics no longer depend on map iteration order ([6497330](https://github.com/cloudfluent/terragraph/commit/64973304956effc90b23a4470dc72cedb661471f))
* destroy refuses --auto-approve when a node's approve level does not permit teardown ([7361b66](https://github.com/cloudfluent/terragraph/commit/7361b66e2bf9562d013b72b09dc66f3b94e734b4))
* destroy's approve gate checks what a node declared ([d91f66a](https://github.com/cloudfluent/terragraph/commit/d91f66aeb834cbea99b878331ef05f9e57230247))
* finish runtime cancellation before releasing graph locks ([#79](https://github.com/cloudfluent/terragraph/issues/79)) ([b0740a6](https://github.com/cloudfluent/terragraph/commit/b0740a6d81774d53f0d9aab6edd922672b4f3333))
* force-unlock works on an unvendored checkout and names the lock holder ([2d9f247](https://github.com/cloudfluent/terragraph/commit/2d9f247a9080a4a71f68b8008c93ed7261e73ebd))
* honor runtime sensitivity in output snapshots ([#72](https://github.com/cloudfluent/terragraph/issues/72)) ([98b68bf](https://github.com/cloudfluent/terragraph/commit/98b68bf8f8eec2cb2a35f4521ec14b7c337b29e8))
* make approval remedies actionable and correct documentation ([#78](https://github.com/cloudfluent/terragraph/issues/78)) ([a045678](https://github.com/cloudfluent/terragraph/commit/a04567878451b9e35ff55f6a49df18dc53a00eba))
* preserve input values and reject unsafe execution inputs ([#70](https://github.com/cloudfluent/terragraph/issues/70)) ([2fc9543](https://github.com/cloudfluent/terragraph/commit/2fc9543678419920c068651791c7d0541a70761b))
* preserve LSP scopes and refresh editor state ([#77](https://github.com/cloudfluent/terragraph/issues/77)) ([34b5c58](https://github.com/cloudfluent/terragraph/commit/34b5c58633c64b5441d18684b4237b3cca1b72de))
* preserve managed node data directory isolation ([#75](https://github.com/cloudfluent/terragraph/issues/75)) ([80e05e5](https://github.com/cloudfluent/terragraph/commit/80e05e579ba35e06f67950ac34fe5257691fd014))
* preserve output sensitivity and consistent input reads ([#88](https://github.com/cloudfluent/terragraph/issues/88)) ([53a2628](https://github.com/cloudfluent/terragraph/commit/53a2628185d5042816bfdce39434d9194b65f7c6))
* preserve vendored package identity and existing state ([#80](https://github.com/cloudfluent/terragraph/issues/80)) ([ea89069](https://github.com/cloudfluent/terragraph/commit/ea89069afe5913ffeaa597556ea45ffc08fc2be8))
* protect saved plans before terraform writes ([#73](https://github.com/cloudfluent/terragraph/issues/73)) ([fd12509](https://github.com/cloudfluent/terragraph/commit/fd1250947fb7a68e2e30d5af0a0b268c598cd00b))
* redact sensitive input validation errors ([#71](https://github.com/cloudfluent/terragraph/issues/71)) ([a7a20cb](https://github.com/cloudfluent/terragraph/commit/a7a20cb4fb1bb8853b3b9630f4abbaab27a7712b))
* support arbitrary HCL filenames in VS Code and clarify docs ([#86](https://github.com/cloudfluent/terragraph/issues/86)) ([a8d89f7](https://github.com/cloudfluent/terragraph/commit/a8d89f7c35c08f15c905fa9160b6c0a06c03a4bd))
* tfvars are owner-only and removed when the run ends ([e1192b3](https://github.com/cloudfluent/terragraph/commit/e1192b37c4dae09c0d62845347b14383eaf5e55c))
* validate graph storage identity and contract types ([#81](https://github.com/cloudfluent/terragraph/issues/81)) ([2865810](https://github.com/cloudfluent/terragraph/commit/286581098b327c1fa11a3ff0aa81d2bc35fa8748))
* warn about relative local backend paths ([#83](https://github.com/cloudfluent/terragraph/issues/83)) ([b820cb4](https://github.com/cloudfluent/terragraph/commit/b820cb481e1a79e38ba9a69ae3bf3cbe76b5abe3))
* warn when a renamed node orphans its local-backend state ([e0d95a9](https://github.com/cloudfluent/terragraph/commit/e0d95a90dd11ff358dc43ed6efa6dcdabda54530))


### Documentation

* add active development warning to README ([#85](https://github.com/cloudfluent/terragraph/issues/85)) ([6f95ed6](https://github.com/cloudfluent/terragraph/commit/6f95ed6f26751805e0b45a46f609deb8f8311756))
* align the user guide with supported behavior ([#89](https://github.com/cloudfluent/terragraph/issues/89)) ([d405b52](https://github.com/cloudfluent/terragraph/commit/d405b52e313ec0cb6ff9f9e375907d50eea4169e))
* correct the write-path invariant in AGENTS.md ([cc28581](https://github.com/cloudfluent/terragraph/commit/cc28581797fb0f4a41c4345b077adb459a8d5418))

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
