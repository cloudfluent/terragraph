package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudfluent/terragraph/internal/engine"
)

// parsePools keeps service limits separate from the existing exact leaf selection syntax.
func parsePools(specs []string) ([]engine.ConcurrencyPool, error) {
	var pools []engine.ConcurrencyPool
	for _, spec := range specs {
		name, raw, ok := strings.Cut(spec, "=")
		count, members, found := strings.Cut(raw, ":")
		limit, err := strconv.Atoi(count)
		if !ok || !found || name == "" || members == "" || err != nil || limit < 1 {
			return nil, engine.WithDiagnostic(fmt.Errorf("--pool %q: use name=positive-limit:node,node", spec), engine.Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments", Subject: "pool"})
		}
		pools = append(pools, engine.ConcurrencyPool{Name: name, Limit: limit, Nodes: strings.Split(members, ",")})
	}
	return pools, nil
}
