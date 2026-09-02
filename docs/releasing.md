# Releasing

Merging conventional commits into `main` lets release-please create a release
PR. Merging that PR creates a `vX.Y.Z` tag and starts the release workflow.
The tag is the single version source for the CLI and the VS Code extension.

The release pipeline does the following:

1. GoReleaser publishes the CLI archives and updates the Homebrew cask.
2. The VS Code workflow cross-compiles `terragraph language-server` for each
   supported VS Code target.
3. Each platform-specific VSIX bundles its matching language-server binary.
4. The VSIX packages are attached to the GitHub Release and published to the
   VS Code Marketplace.

The extension therefore works without the CLI being installed separately. Its
`terragraph.languageServer.path` setting remains an explicit override.

## Marketplace setup

Before the first release, create the `cloudfluent` publisher in Visual Studio
Marketplace. It must match the `publisher` field in
`editors/vscode/package.json`.

Create an Azure DevOps Personal Access Token with **Marketplace (Manage)**
scope and add it to this repository's GitHub Actions secrets as `VSCE_PAT`.
The reusable `Publish VS Code extension` workflow receives that secret from
both release entry points and uses it only for `vsce publish`.

Azure DevOps retires global PATs on 2026-12-01. Before that date, replace this
credential with the Marketplace's Microsoft Entra ID workload-identity flow.
See the [VS Code publishing documentation](https://code.visualstudio.com/api/working-with-extensions/publishing-extension)
for the publisher setup and migration procedure.

## Local checks

Run all Go and VS Code extension checks with:

```sh
make check
```

To smoke-test one platform package locally, first build the server into the
extension bundle and then package it:

```sh
go build -o editors/vscode/bin/terragraph ./cmd/terragraph
cd editors/vscode
npm run package -- --target darwin-arm64
```

Choose a target that matches the binary you built. CI creates all supported
macOS, Linux, and Windows x64/arm64 packages.
