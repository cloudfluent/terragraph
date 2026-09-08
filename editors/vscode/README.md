# Terragraph Blueprint for VS Code

This extension starts `terragraph language-server` and supplies completion with port metadata, definition navigation, and diagnostics for Terragraph `.hcl` files opened as HCL. Filenames are flexible: `nodes.hcl`, `edges.hcl`, `contracts.hcl`, and files in group source directories receive the same editing support as the conventional `blueprint.hcl` and `group.hcl` names.

Open-file edits are sent to the server without saving. It also reads neighboring `.hcl` files for context. When running a split blueprint in the CLI, use `--blueprint .`; omitting the flag still loads only `blueprint.hcl`. See [files and loading](../../docs/blueprint.md#files-and-loading).

Marketplace releases contain a matching `terragraph language-server` binary, so no separate CLI installation is required for editor features. Set `terragraph.languageServer.path` only to override that bundled binary.

During development:

```sh
cd editors/vscode
npm install
npm run compile
```

For a source checkout, the extension automatically uses the executable built at the repository root. You can also explicitly choose a binary, for example:

```json
"terragraph.languageServer.path": "/absolute/path/to/terragraph"
```

Use VS Code's **Run Extension** launch configuration, then open a Terragraph workspace. The server currently completes local Terraform module inputs and outputs, node/use traversals, and direct group `export` ports. It deliberately continues providing completion while the HCL document is syntactically incomplete.

## Integration tests

From the repository root, `make vscode-check` builds the language server and runs the extension checks, including tests in a real VS Code Extension Host. `make check` includes the same tests. For a narrower loop after building the server, run `npm test` in this directory.

The runner downloads VS Code on its first run and caches it under `.vscode-test/`. To use an existing installation, set `TERRAGRAPH_VSCODE_EXECUTABLE` to its executable path. Each run uses a temporary workspace, profile, and extensions directory. A fixture extension registers the HCL language; the real Terragraph extension and language server provide completion metadata, definition navigation, and diagnostics, including edits that have not been saved.

On Linux without a display, run `xvfb-run -a make check` from the repository root, as CI does (requires Xvfb).
