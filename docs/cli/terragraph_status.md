## terragraph status

Observe current node status with isolated runtime context

```
terragraph status [flags]
```

### Options

```
  -h, --help            help for status
      --node string     restrict to an exact expanded leaf node
      --output string   output format: text or json (default "text")
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph](terragraph.md)	 - Graph-based orchestration for independent Terraform/OpenTofu root modules
