# VS Code IntelliSense

The Terragraph Blueprint extension gives `blueprint.hcl` and `group.hcl` language-server-backed editing. Install **Terragraph Blueprint** from the VS Code Marketplace and it works immediately.

The extension bundles a compatible language server, so nothing here requires installing the `terragraph` CLI separately.

## Completion

Open the completion list with `Ctrl+Space` (`Control+Space` on macOS).

- Top-level blueprint blocks: `node`, `edge`, `runtime`, `group`, `use`, `vendor`, `tfvars`
- The attributes each block accepts: a node's `source`, `vars`, `env`, `runtime`, `backend_config`, and so on
- A Terraform/OpenTofu module's own input variables and outputs
- Declared runtime names

Inside `vars = {}` only the input variables the node's own module declares are suggested. To pass another node's result, use an `edge` rather than `vars`.

```hcl
edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
```

An input suggestion shows its type, whether it's required, whether it's sensitive, and its description. An output shows its name, description, and whether it's sensitive.

An edge's nested `input` blocks (see [blueprint.md](blueprint.md#several-values-between-the-same-two-nodes-input)) complete the same way: the block label suggests the input variables declared by the edge's `to` node, and `from = output.` suggests the outputs of its `from` node. Neither reference repeats a node name, so both suggestion lists come from that edge's own endpoints.

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

`Cmd+Click` (macOS), `Ctrl+Click` (Windows/Linux), or `F12` on `node.vpc` or `runtime.tofu` jumps to its declaration, including nodes and runtimes declared in another `.hcl` file in the same blueprint directory.

## Error reporting

Mistakes that can be found without evaluating anything are underlined as you type:

- A node name that isn't declared
- A module input or output name that doesn't exist
- A `from` referencing an input, or a `to` referencing an output
- A `vars` entry the module has no such input variable for
- An edge `input` block whose label isn't an input of the `to` node, or whose `from = output.<attr>` isn't an output of the `from` node

Hovering an error lists the input or output names that are available.

## Pointing at your own language server

Rarely needed, but to use a binary you're developing or a specific CLI version, set this in your VS Code settings:

```json
{
  "terragraph.languageServer.path": "/absolute/path/to/terragraph"
}
```

Clearing the path goes back to the language server bundled with the extension.

## When completion doesn't appear

1. Check the file is named `blueprint.hcl` or `group.hcl`.
2. Reload the window with the `Developer: Reload Window` command.
3. Under **View: Output**, select the `Terragraph Blueprint` channel and look for a language server startup error.
4. If you're developing, run `make build` in the repository root and restart the Extension Development Host.
