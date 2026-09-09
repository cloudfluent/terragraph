## terragraph plugin recover

Retry a recorded idempotent plugin effect or acknowledge its externally reviewed outcome

```
terragraph plugin recover EXECUTION_ID CALL_ID [flags]
```

### Options

```
      --acknowledge-external-state   record that the external outcome was inspected and resolved without replaying it
      --confirm-stopped              confirm the previous executor has stopped
  -h, --help                         help for recover
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph plugin](terragraph_plugin.md)	 - Install and inspect version-locked executable plugins
