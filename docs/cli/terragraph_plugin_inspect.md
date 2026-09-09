## terragraph plugin inspect

Read package metadata and checksums without executing plugin code

```
terragraph plugin inspect PACKAGE_DIRECTORY [flags]
```

### Options

```
  -h, --help   help for inspect
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph plugin](terragraph_plugin.md)	 - Install and inspect version-locked executable plugins
