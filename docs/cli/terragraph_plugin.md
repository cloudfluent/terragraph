## terragraph plugin

Install and inspect version-locked executable plugins

### Options

```
  -h, --help   help for plugin
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
* [terragraph plugin inspect](terragraph_plugin_inspect.md)	 - Read package metadata and checksums without executing plugin code
* [terragraph plugin install](terragraph_plugin_install.md)	 - Install a local release package and pin its checksum for this platform
* [terragraph plugin list](terragraph_plugin_list.md)	 - Verify and list locked plugins for this platform without executing them
