package engine

import (
	"fmt"
	"sort"

	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/pathidentity"
)

// pathCollisions uses the same path functions as execution so aliased artifact directories cannot turn two nodes into one backend cache or plan file.
func (e *Engine) pathCollisions() []graph.Problem {
	if e.BaseDir == "" || e.Blueprint == nil {
		return nil
	}
	names := make([]string, 0, len(e.Graph.Nodes))
	for name := range e.Graph.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	paths := []func(string) string{e.dataDir, e.planPath, e.tfVarsPath}
	if e.Graph.Snapshots {
		paths = append(paths, e.snapshotPath)
	}
	var problems []graph.Problem
	for i, a := range names {
		for _, b := range names[i+1:] {
			unverified, reported := false, false
			for _, path := range paths {
				same, known, err := pathidentity.Same(path(a), path(b))
				if err != nil {
					problems = append(problems, graph.Problem{Severity: graph.SeverityError, Message: fmt.Sprintf("node.%s and node.%s: checking managed paths: %v; make their parent directories accessible", a, b, err)})
					reported = true
					break
				}
				if same && known {
					problems = append(problems, graph.Problem{Severity: graph.SeverityError, Message: fmt.Sprintf("node.%s and node.%s resolve to the same managed path on this filesystem; choose distinct names or remove the path alias", a, b)})
					reported = true
					break
				}
				if !known {
					unverified = true
				}
			}
			if unverified && !reported {
				problems = append(problems, graph.Problem{Severity: graph.SeverityWarning, Message: fmt.Sprintf("node.%s and node.%s: filesystem path separation could not be verified; use names that differ beyond letter case or verify their parent directories", a, b)})
			}
		}
	}
	return problems
}
