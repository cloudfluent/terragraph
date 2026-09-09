## terragraph plugin install

Install a local release package and pin its checksum for this platform

```
terragraph plugin install ALIAS PACKAGE_DIRECTORY [flags]
```

### Options

```
  -h, --help     help for install
      --locked   require the package to match the existing lock without changing it
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph plugin](terragraph_plugin.md)	 - Install and inspect version-locked executable plugins
