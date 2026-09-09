## terragraph plugin report

Write a plugin-authored report to stdout; may contain sensitive plan evidence

```
terragraph plugin report EXECUTION_ID CALL_ID [flags]
```

### Options

```
  -h, --help   help for report
```

### Options inherited from parent commands

```
      --blueprint string   path to a blueprint file or a directory whose .hcl files are merged, excluding .terraform.lock.hcl (default ".")
      --log-level string   log verbosity for internal diagnostics on stderr: debug, info, warn, or error (default "warn")
      --tofu               use the tofu binary instead of terraform
```

### SEE ALSO

* [terragraph plugin](terragraph_plugin.md)	 - Install and inspect version-locked executable plugins
