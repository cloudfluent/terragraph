package graph

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity distinguishes a hard failure from an advisory finding.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

// Problem is one validation issue found in a Graph. Validate collects all of them in one pass so a user fixing typos in a large blueprint sees every mistake at once instead of one-at-a-time.
type Problem struct {
	Code, Subject, Remedy string
	Severity              Severity
	Message               string
}

// IsError reports whether this problem should block graph/plan/apply, as opposed to a Warning, which is only surfaced.
func (p Problem) IsError() bool { return p.Severity == SeverityError }

// Validate checks a built Graph for issues that Build itself does not catch: edges referencing ports that don't actually exist on the target module (Error), two data edges targeting the same input, whether their from sides differ or match (Error; checked here rather than at parse so group expansion can rewrite a use export onto leaf ports first), a node's own vars targeting a variable the module doesn't declare or one a data edge already targets (Error), cycles in the dependency graph (Error), required variables that neither an edge nor vars ever supplies a value for (Warning: a module may legitimately get such a value from its own terraform.tfvars or the environment, outside the blueprint entirely), a non-empty backend_config on a module with no backend block (Error), two or more leaves that share a module directory and resolve to identical backend_config maps (Error), and, when a graph remote lock is set, a node whose backend is not remote or whose state key equals the graph lock key (Error).
func Validate(g *Graph) []Problem {
	var problems []Problem

	wired := make(map[string]bool)
	reportedMulti := make(map[string]bool)
	for _, e := range g.Edges {
		if !e.IsDataEdge() {
			continue
		}
		from := g.Nodes[e.From.Node]
		if !from.Schema.HasOutput(e.From.Name) {
			problems = append(problems, Problem{
				Code: "missing_output", Subject: e.From.String(), Remedy: "declare the referenced output or correct the edge",
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: node %q has no output named %q", e.From, e.From.Node, e.From.Name),
			})
		}
		to := g.Nodes[e.To.Node]
		if !to.Schema.HasVariable(e.To.Name) {
			problems = append(problems, Problem{
				Code: "missing_input", Subject: e.To.String(), Remedy: "declare the referenced variable or correct the edge",
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: node %q has no input variable named %q", e.To, e.To.Node, e.To.Name),
			})
		}
		key := e.To.Node + "." + e.To.Name
		if wired[key] {
			if !reportedMulti[key] {
				problems = append(problems, Problem{
					Code: "input_conflict", Subject: "node." + e.To.Node + ".input." + e.To.Name, Remedy: "remove extra data edges",
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s.input.%s: set by more than one data edge; remove extras", e.To.Node, e.To.Name),
				})
				reportedMulti[key] = true
			}
			continue
		}
		wired[key] = true
	}

	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		node := g.Nodes[name]
		keys := make([]string, 0, len(node.Vars))
		for varName := range node.Vars {
			keys = append(keys, varName)
		}
		sort.Strings(keys)
		for _, varName := range keys {
			if !node.Schema.HasVariable(varName) {
				problems = append(problems, Problem{
					Code: "missing_input", Subject: "node." + name + ".input." + varName, Remedy: "remove the vars entry or declare the variable",
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s.input.%s: vars sets it, but node %q has no such input variable", name, varName, name),
				})
				continue
			}
			key := name + "." + varName
			if wired[key] {
				problems = append(problems, Problem{
					Code: "input_conflict", Subject: "node." + name + ".input." + varName, Remedy: "remove the data edge or vars entry",
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s.input.%s: set by both a data edge and vars; remove one", name, varName),
				})
				continue
			}
			wired[key] = true
		}
	}

	for _, name := range names {
		node := g.Nodes[name]
		for input := range node.Inputs {
			subject := "node." + name + ".input." + input
			if v, exists := node.Schema.Variables[input]; exists && !v.Sensitive {
				problems = append(problems, Problem{Code: "plugin_input_not_sensitive", Subject: subject, Severity: SeverityError, Message: subject + ": plugin inputs require sensitive = true on the module variable"})
			}
			if wired[name+"."+input] {
				problems = append(problems, Problem{Code: "input_conflict", Subject: subject, Severity: SeverityError, Message: subject + ": plugin input conflicts with vars or edge; remove one supplier"})
			}
			if !node.Schema.HasVariable(input) {
				problems = append(problems, Problem{Code: "missing_input", Subject: subject, Severity: SeverityError, Message: subject + ": no such module variable; correct the binding"})
			}
			wired[name+"."+input] = true
		}
	}

	for _, name := range names {
		node := g.Nodes[name]
		varNames := make([]string, 0, len(node.Schema.Variables))
		for varName := range node.Schema.Variables {
			varNames = append(varNames, varName)
		}
		sort.Strings(varNames)
		for _, varName := range varNames {
			v := node.Schema.Variables[varName]
			if v.Required && !wired[name+"."+varName] {
				problems = append(problems, Problem{
					Code: "required_input_unwired", Subject: "node." + name + ".input." + varName, Remedy: "supply the variable through an edge, vars, a plugin input binding, or runtime input",
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("node.%s.input.%s: required variable %q has no edge feeding it (must be supplied some other way, e.g. terraform.tfvars)", name, varName, varName),
				})
			}
		}
	}

	for _, cycle := range FindCycles(g) {
		problems = append(problems, Problem{
			Code: "dependency_cycle", Subject: "graph", Remedy: "remove an edge in the reported cycle",
			Severity: SeverityError,
			Message:  fmt.Sprintf("cycle detected: %s", strings.Join(cycle, " -> ")),
		})
	}

	problems = append(problems, backendProblems(g)...)
	problems = append(problems, knownBackendProblems(g)...)
	problems = append(problems, remoteLockProblems(g)...)
	problems = append(problems, contractProblems(g)...)

	return problems
}

var remoteLockBackends = map[string]bool{
	"s3":      true,
	"gcs":     true,
	"azurerm": true,
	"http":    true,
	"remote":  true,
	"cloud":   true,
}

func remoteLockProblems(g *Graph) []Problem {
	if g == nil || g.Lock == nil {
		return nil
	}

	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []Problem
	for _, name := range names {
		node := g.Nodes[name]
		backend := ""
		if node.Schema != nil {
			backend = node.Schema.Backend
		}
		if !remoteLockBackends[backend] {
			what := "this module has no backend block"
			if backend != "" {
				what = fmt.Sprintf("this module has backend %q", backend)
			}
			problems = append(problems, Problem{
				Code: "remote_backend_required", Subject: "node." + name, Remedy: "use a remote state backend with a remote graph lock",
				Severity: SeverityError,
				Message:  fmt.Sprintf("node.%s: remote lock requires a remote backend (s3, gcs, azurerm, http, remote, or cloud); %s", name, what),
			})
		}
		// Keep the existing conservative check for an explicit partial key, and use complete literal addresses when the module supplies them.
		collision := g.Lock.S3 != nil && g.Lock.S3.Key != "" && backend == "s3" &&
			node.BackendConfig["key"] == g.Lock.S3.Key &&
			(node.BackendConfig["bucket"] == "" || node.BackendConfig["bucket"] == g.Lock.S3.Bucket)
		possibleCollision := false
		if address, known := s3Address(node); g.Lock.S3 != nil {
			endpoint := os.Getenv("AWS_ENDPOINT_URL_S3")
			if endpoint == "" {
				endpoint = os.Getenv("AWS_ENDPOINT_URL")
			}
			lockAddress := s3StateAddress{bucket: g.Lock.S3.Bucket, key: g.Lock.S3.Key, endpoint: endpoint, partition: awsPartition(g.Lock.S3.Region)}
			if known {
				collision = address == lockAddress
			}
			possibleCollision = possibleS3Collision(address, lockAddress)
		}
		if collision {
			problems = append(problems, Problem{
				Code: "lock_state_conflict", Subject: "node." + name, Remedy: "use distinct keys for graph lock and state",
				Severity: SeverityError,
				Message:  fmt.Sprintf("node.%s: graph lock key %q must not be a node's state key", name, g.Lock.S3.Key),
			})
		} else if possibleCollision {
			problems = append(problems, Problem{
				Code: "lock_state_unverified", Subject: "node." + name, Remedy: "verify distinct lock and state addresses",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("node.%s: graph lock may share the node's s3 state because the AWS partition is not statically known; set explicit backend region and endpoint settings where applicable and verify that the resolved lock and state namespaces are distinct", name),
			})
		}
	}
	return problems
}

func backendProblems(g *Graph) []Problem {
	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []Problem
	byDir := map[string][]string{}
	for _, name := range names {
		node := g.Nodes[name]
		if len(node.BackendConfig) > 0 {
			backend := ""
			if node.Schema != nil {
				backend = node.Schema.Backend
			}
			if backend == "" || backend == "cloud" {
				problems = append(problems, Problem{
					Code: "backend_missing", Subject: "node." + name, Remedy: "declare a backend block or remove backend_config",
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s: backend_config is set but the module declares no backend block", name),
				})
			}
		}
		// Terraform resolves explicit relative paths from the module directory, which can put state inside a vendored source tree.
		if statePath := node.BackendConfig["path"]; node.Schema != nil && node.Schema.Backend == "local" && statePath != "" && !filepath.IsAbs(statePath) {
			problems = append(problems, Problem{
				Code: "relative_state_path", Subject: "node." + name + ".backend_config.path", Remedy: "use an absolute state path outside the module",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("node.%s.backend_config.path: relative path %q for the default workspace is resolved from module directory %q, not the blueprint directory; use an absolute path outside the module or omit backend_config.path to use the managed state path", name, statePath, node.Dir),
			})
		}
		if node.Dir != "" {
			byDir[node.Dir] = append(byDir[node.Dir], name)
		}
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		nodesInDir := byDir[dir]
		if len(nodesInDir) < 2 {
			continue
		}
		type partition struct {
			cfg   map[string]string
			names []string
		}
		var parts []partition
		for _, name := range nodesInDir {
			cfg := g.Nodes[name].BackendConfig
			found := false
			for i := range parts {
				if maps.Equal(parts[i].cfg, cfg) {
					parts[i].names = append(parts[i].names, name)
					found = true
					break
				}
			}
			if !found {
				parts = append(parts, partition{cfg: cfg, names: []string{name}})
			}
		}
		for _, p := range parts {
			if len(p.names) < 2 {
				continue
			}
			problems = append(problems, Problem{
				Code: "backend_conflict", Subject: dir, Remedy: "configure distinct backend addresses for these nodes",
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"%s: nodes %s share this module directory but resolve to identical backend_config",
					dir, strings.Join(p.names, ", "),
				),
			})
		}
	}
	return problems
}

// FindCycle returns the node names in one cyclic strongly-connected component, or nil if the graph is acyclic. Kept for callers that just want a short, single-cycle error message (topo.go); Validate uses FindCycles to report every cyclic cluster in the graph, not just one.
func FindCycle(g *Graph) []string {
	cycles := FindCycles(g)
	if len(cycles) == 0 {
		return nil
	}
	return cycles[0]
}
