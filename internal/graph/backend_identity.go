package graph

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/pathidentity"
)

// effectiveBackendConfig keeps Terraform's own literals separate from init overrides while letting address checks see their combined result.
func effectiveBackendConfig(n *Node) map[string]string {
	if n.Schema == nil {
		return n.BackendConfig
	}
	return mergeEnv(n.Schema.BackendConfig, n.BackendConfig)
}

func nodeEnvironment(n *Node, key string) string {
	if value, ok := n.Env[key]; ok {
		return value
	}
	return os.Getenv(key)
}

// localStatePath models only known local addresses; backup files and other non-address options cannot make a shared state safe.
func localStatePath(n *Node) (string, bool) {
	if n.Schema == nil || !n.Schema.BackendConfigKnown || (n.Schema.Backend != "" && n.Schema.Backend != "local") || n.Dir == "" {
		return "", false
	}
	cfg := effectiveBackendConfig(n)
	state := cfg["path"]
	if state == "" {
		state = "terraform.tfstate"
	}
	if workspace := nodeEnvironment(n, "TF_WORKSPACE"); workspace != "" && workspace != "default" {
		dir := cfg["workspace_dir"]
		if dir == "" {
			dir = "terraform.tfstate.d"
		}
		state = filepath.Join(dir, workspace, "terraform.tfstate")
	}
	if !filepath.IsAbs(state) {
		state = filepath.Join(n.Dir, state)
	}
	return state, true
}

type s3StateAddress struct{ bucket, key, endpoint, partition string }

// s3Address retains literal bucket/key candidates when the partition is unknown, but only reports a complete address as known.
func s3Address(n *Node) (s3StateAddress, bool) {
	if n.Schema == nil || !n.Schema.BackendConfigKnown || n.Schema.Backend != "s3" {
		return s3StateAddress{}, false
	}
	cfg := effectiveBackendConfig(n)
	if cfg["bucket"] == "" || cfg["key"] == "" {
		return s3StateAddress{}, false
	}
	key := cfg["key"]
	if workspace := nodeEnvironment(n, "TF_WORKSPACE"); workspace != "" && workspace != "default" {
		prefix, set := cfg["workspace_key_prefix"]
		if !set {
			prefix = "env:"
		}
		key = path.Join(prefix, workspace, key)
	}
	endpoint := cfg["endpoint"]
	if endpoint == "" {
		endpoint = nodeEnvironment(n, "AWS_ENDPOINT_URL_S3")
	}
	if endpoint == "" {
		endpoint = nodeEnvironment(n, "AWS_ENDPOINT_URL")
	}
	region := cfg["region"]
	if region == "" {
		region = nodeEnvironment(n, "AWS_REGION")
	}
	if region == "" {
		region = nodeEnvironment(n, "AWS_DEFAULT_REGION")
	}
	partition := awsPartition(region)
	return s3StateAddress{bucket: cfg["bucket"], key: key, endpoint: endpoint, partition: partition}, partition != ""
}

// possibleS3Collision warns about unresolved partitions without treating profiles or a matching custom endpoint as proof of one namespace.
func possibleS3Collision(a, b s3StateAddress) bool {
	return a.bucket != "" && a.key != "" && a.bucket == b.bucket && a.key == b.key &&
		(a.endpoint == "" || b.endpoint == "" || a.endpoint == b.endpoint) &&
		(a.partition == "" || b.partition == "")
}

func awsPartition(region string) string {
	if region == "" || strings.Contains(region, "-iso") {
		return ""
	}
	if strings.HasPrefix(region, "cn-") {
		return "aws-cn"
	}
	if strings.HasPrefix(region, "us-gov-") {
		return "aws-us-gov"
	}
	return "aws"
}

// knownBackendProblems compares actual addresses across source directories without replacing source-based contracts or guessing expression values.
func knownBackendProblems(g *Graph) []Problem {
	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	var problems []Problem
	for i, aName := range names {
		a := g.Nodes[aName]
		for _, bName := range names[i+1:] {
			b := g.Nodes[bName]
			if a.Dir == b.Dir && maps.Equal(a.BackendConfig, b.BackendConfig) {
				continue
			}
			if aPath, okA := localStatePath(a); okA {
				if bPath, okB := localStatePath(b); okB {
					same, known, err := pathidentity.Same(aPath, bPath)
					if err != nil {
						problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("node.%s and node.%s: checking local state paths: %v; make their parent directories accessible", aName, bName, err)})
					} else if known && same {
						problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("node.%s and node.%s resolve to the same local state; set distinct backend_config.path values or remove the source/path alias", aName, bName)})
					} else if !known {
						problems = append(problems, Problem{Severity: SeverityWarning, Message: fmt.Sprintf("node.%s and node.%s: local state path separation could not be verified; use distinct paths or verify their parent directories", aName, bName)})
					}
				}
			}
			aAddr, knownA := s3Address(a)
			bAddr, knownB := s3Address(b)
			if knownA && knownB && aAddr == bAddr {
				problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("node.%s and node.%s resolve to the same s3 state; set distinct backend bucket/key addresses", aName, bName)})
			} else if possibleS3Collision(aAddr, bAddr) {
				problems = append(problems, Problem{Severity: SeverityWarning, Message: fmt.Sprintf("node.%s and node.%s may resolve to the same s3 state because the AWS partition is not statically known; set explicit backend region and endpoint settings where applicable and verify that the resolved bucket/key namespaces are distinct", aName, bName)})
			}
		}
	}
	return problems
}
