# Terragraph Blueprint for VS Code

This extension starts `terragraph language-server` and supplies Blueprint-aware
completion for `blueprint.hcl` and `group.hcl` files opened as HCL.

Marketplace releases contain a matching `terragraph language-server` binary,
so no separate CLI installation is required for editor features. Set
`terragraph.languageServer.path` only to override that bundled binary.

During development:

```sh
cd editors/vscode
npm install
npm run compile
```

For a source checkout, the extension automatically uses the executable built
at the repository root. You can also explicitly choose a binary, for example:

```json
"terragraph.languageServer.path": "/absolute/path/to/terragraph"
```

Use VS Code's **Run Extension** launch configuration, then open a Terragraph
workspace. The server currently completes local Terraform module inputs and
outputs, node/use traversals, and direct group `export` ports. It deliberately
continues providing completion while the HCL document is syntactically
incomplete.
