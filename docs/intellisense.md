# VS Code IntelliSense

Install **Terragraph** (`cloudfluent.terragraph-vscode`) from the VS Code Marketplace and open Terragraph `.hcl` files with the **HCL** language mode. Filenames can be anything, including `nodes.hcl`, `edges.hcl`, `contracts.hcl`, and files in group source directories. The extension bundles its language server, so editor features do not require a separate CLI installation.

The editor uses other `.hcl` files in the same directory as context, including unsaved changes in open files. To run a split blueprint through the CLI, use `--blueprint .`: the CLI's default still reads only `blueprint.hcl`. See [files and loading](blueprint.md#files-and-loading).

## Completion

Open the completion list with `Ctrl+Space` (`Control+Space` on macOS). Suggestions include:

- Top-level blocks: `node`, `edge`, `runtime`, `group`, `use`, `vendor`, `tfvars`, `lock`, `snapshots`
- Block attributes, such as a node's `source`, `vars`, `env`, `runtime`, `backend_config`, and `approve`
- Declared node and runtime names, group instance references, and exported group ports
- Local Terraform/OpenTofu module inputs and outputs, with descriptions and sensitivity; inputs also show type and whether they are required

Inside a node's `vars = {}`, suggestions come from that module's inputs. Inside a `use` block's `vars = {}`, they come from the group's exported inputs. To pass another node's result, use an edge:

```hcl
edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
```

An edge's [nested `input` blocks](blueprint.md#several-values-between-the-same-two-nodes-input) also complete against its endpoints: input labels come from the `to` node, and `output.` suggestions come from the `from` node.

```hcl
edge {
  from = node.vpc
  to   = node.eks

  input "vpc_id" {
    from = output.vpc_id
  }
}
```

## Go to definition

`Cmd+Click` (macOS), `Ctrl+Click` (Windows/Linux), or `F12` on `node.vpc` or `runtime.tofu` jumps to its declaration, including declarations in another `.hcl` file in the same directory.

## Error reporting and limits

The editor underlines unknown node names, nonexistent module ports, incorrect input/output directions, invalid `vars` keys, and invalid edge `input` mappings as you type. For unknown ports, hovering the error lists the available names.

Module-port completion and name checks require a readable local module. A node with a remote `source` has no module-port completion or name checks, **even after vendoring**; node-name and block completion still work.

Module inspection follows a node's explicit runtime or the root blueprint's default, with Terraform as the editor fallback. Inside a group definition, a node without an explicit runtime has known module ports only when Terraform and OpenTofu expose the same declarations: its runtime depends on the use site, not a default-marked runtime in the group's source directory. Ambiguous wrappers or unreadable modules leave ports unknown. Editor inspection never executes a runtime.

Contract-specific completion and contract checks for `producer`, `consumer`, and `contracts` blocks are not implemented. A file named `contracts.hcl` receives the same editing support for node/edge content as any other `.hcl` file; its name does not add contract support.

Editor diagnostics cover these editing checks, not the full CLI validation. Use `terragraph validate` to check the blueprint, including vendored modules and contracts. For a split blueprint:

```sh
terragraph validate --blueprint .
```

## Pointing at your own language server

To use a binary you are developing or a specific CLI version, set:

```json
{
  "terragraph.languageServer.path": "/absolute/path/to/terragraph"
}
```

Clearing the path restores the bundled server. A source checkout uses its locally built binary when no bundled server is present.

## When completion doesn't appear

1. Check that the file has a `.hcl` extension and the status bar language mode is **HCL**; its basename can be anything.
2. For missing port suggestions, check the source and runtime limits above.
3. Reload the window with `Developer: Reload Window`.
4. Under **View: Output**, select the `Terragraph Blueprint` channel and look for a language server startup error.
5. If you are developing, run `make build` in the repository root and restart the Extension Development Host.
