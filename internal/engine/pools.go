package engine

import "fmt"

// ConcurrencyPool limits simultaneous users of a shared service without changing selection or graph ordering.
type ConcurrencyPool struct {
	Name  string
	Limit int
	Nodes []string
}

func (e *Engine) validatePools(opts Options) error {
	fail := func(err error) error {
		return WithDiagnostic(err, Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "selection", Subject: "pool"})
	}
	names := map[string]bool{}
	for _, pool := range opts.Pools {
		if pool.Name == "" || names[pool.Name] || pool.Limit < 1 || len(pool.Nodes) == 0 {
			return fail(fmt.Errorf("pool.%s: supply a unique name, positive limit, and at least one leaf", pool.Name))
		}
		names[pool.Name] = true
		seen := map[string]bool{}
		for _, name := range pool.Nodes {
			if e.Graph.Nodes[name] == nil || seen[name] {
				return fail(fmt.Errorf("pool.%s: unknown or repeated node %q; list each expanded leaf once", pool.Name, name))
			}
			seen[name] = true
		}
	}
	return nil
}
