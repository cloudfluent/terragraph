## terragraph run

Run a supported native operation in one node's runtime and backend context

```
terragraph run --node <leaf> -- <native-command> [arguments] [flags]
```

### Options

```
  -h, --help          help for run
      --node string   one exact expanded leaf; groups are not expanded
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
