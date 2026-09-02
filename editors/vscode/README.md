# Terragraph Blueprint for VS Code

This extension starts `terragraph language-server` and supplies Blueprint-aware
completion for `blueprint.hcl` and `group.hcl` files opened as HCL.

Install the `terragraph` executable somewhere on `PATH`, or set
`terragraph.languageServer.path` to its absolute path. During development:

```sh
cd editors/vscode
npm install
npm run compile
```

For a source checkout, use the executable built at the repository root, for
example:

```json
"terragraph.languageServer.path": "/absolute/path/to/terragraph"
```

Use VS Code's **Run Extension** launch configuration, then open a Terragraph
workspace. The server currently completes local Terraform module inputs and
outputs, node/use traversals, and direct group `export` ports. It deliberately
continues providing completion while the HCL document is syntactically
incomplete.
