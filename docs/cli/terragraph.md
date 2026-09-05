## terragraph

Graph-based orchestration for independent Terraform/OpenTofu root modules

### Options

```
      --blueprint string   path to the blueprint file, or a directory whose .hcl files are merged into one blueprint (default "blueprint.hcl")
  -h, --help               help for terragraph
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
  -v, --version            version for terragraph
```

### SEE ALSO

* [terragraph apply](terragraph_apply.md)	 - Run terraform/tofu apply across the graph in dependency order, wiring outputs to inputs
* [terragraph destroy](terragraph_destroy.md)	 - Run terraform/tofu destroy across the graph in reverse dependency order
* [terragraph force-unlock](terragraph_force-unlock.md)	 - Release a leftover graph lock object left by an interrupted run
* [terragraph graph](terragraph_graph.md)	 - Print execution dependencies or architectural relationships
* [terragraph language-server](terragraph_language-server.md)	 - Run the Blueprint language server over standard input/output
* [terragraph plan](terragraph_plan.md)	 - Run terraform/tofu plan across the graph in dependency order
* [terragraph validate](terragraph_validate.md)	 - Parse the blueprint and check it against the real module schemas
* [terragraph vendor](terragraph_vendor.md)	 - Fetch remote node sources into a local, committable directory
