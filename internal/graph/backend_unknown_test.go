package graph

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// s3PartitionFixture excludes ambient AWS routing so unknown-region cases cannot accidentally become confirmed collisions on the test host.
func s3PartitionFixture(t *testing.T, a, b string) string {
	t.Helper()
	for _, key := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ENDPOINT_URL_S3", "AWS_ENDPOINT_URL", "TF_WORKSPACE"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	for name, extra := range map[string]string{"a": a, "b": b} {
		writeBackendModule(t, filepath.Join(root, name), fmt.Sprintf(`terraform {
 backend "s3" {
  bucket = "shared-bucket"
  key = "shared.tfstate"
  %s
 }
}`, extra))
	}
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" { source = "./a" }
node "b" { source = "./b" }
`)
	return root
}

func requireS3PartitionWarning(t *testing.T, problems []Problem) {
	t.Helper()
	if len(problems) != 1 || problems[0].Severity != SeverityWarning || !strings.Contains(problems[0].Message, "may resolve to the same s3 state") || !strings.Contains(problems[0].Message, "partition") || !strings.Contains(problems[0].Message, "region") || !strings.Contains(problems[0].Message, "endpoint") {
		t.Fatalf("got = %v, want one possible s3 collision warning with partition and region/endpoint remedy", problems)
	}
}

func TestValidate_RegionlessS3AddressWarns(t *testing.T) {
	root := s3PartitionFixture(t, "", "")
	requireS3PartitionWarning(t, validateLockFixture(t, root))
}

func TestValidate_S3ProfilesWithUnresolvedPartitionsWarn(t *testing.T) {
	root := s3PartitionFixture(t, `profile = "china"`, `profile = "gov"`)
	config := filepath.Join(root, "aws-config")
	writeFixtureFile(t, config, "[profile china]\nregion = cn-north-1\n[profile gov]\nregion = us-gov-west-1\n")
	t.Setenv("AWS_CONFIG_FILE", config)
	requireS3PartitionWarning(t, validateLockFixture(t, root))
}

func TestValidate_S3OneUnknownPartitionWarns(t *testing.T) {
	root := s3PartitionFixture(t, `region = "us-east-1"`, "")
	requireS3PartitionWarning(t, validateLockFixture(t, root))
}

func TestValidate_S3MatchingEndpointDoesNotProveUnknownPartition(t *testing.T) {
	root := s3PartitionFixture(t, `endpoint = "https://s3.example.invalid"`, `endpoint = "https://s3.example.invalid"`)
	requireS3PartitionWarning(t, validateLockFixture(t, root))
}

func TestValidate_S3UnknownEndpointDoesNotProveSeparation(t *testing.T) {
	root := s3PartitionFixture(t, `endpoint = "https://s3.example.invalid"`, "")
	requireS3PartitionWarning(t, validateLockFixture(t, root))
}

func TestValidate_S3KnownDistinctPartitionsStayValid(t *testing.T) {
	root := s3PartitionFixture(t, `region = "cn-north-1"`, `region = "us-gov-west-1"`)
	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("got = %v, want distinct known partitions to remain valid", problems)
	}
}

func TestValidate_S3KnownSamePartitionRemainsError(t *testing.T) {
	root := s3PartitionFixture(t, `region = "us-east-1"`, `region = "eu-west-1"`)
	problems := validateLockFixture(t, root)
	if len(problems) != 1 || problems[0].Severity != SeverityError || !hasErrorContaining(problems, "same s3 state") {
		t.Fatalf("got = %v, want one known shared s3 state error", problems)
	}
}

func TestValidate_S3KnownDistinctEndpointsStayValid(t *testing.T) {
	root := s3PartitionFixture(t, `endpoint = "https://one.example.invalid"`, `endpoint = "https://two.example.invalid"`)
	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("got = %v, want distinct explicit endpoints to remain valid", problems)
	}
}

func TestValidate_RegionlessS3BucketOverrideStaysDistinct(t *testing.T) {
	root := s3PartitionFixture(t, "", "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" { source = "./a" }
node "b" {
 source = "./b"
 backend_config = { bucket = "other-bucket" }
}
`)
	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("got = %v, want distinct effective buckets to remain valid", problems)
	}
}

func TestValidate_RegionlessS3WorkspacesStayDistinct(t *testing.T) {
	root := s3PartitionFixture(t, "", "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" {
 source = "./a"
 env = { TF_WORKSPACE = "dev" }
}
node "b" {
 source = "./b"
 env = { TF_WORKSPACE = "prod" }
}
`)
	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("got = %v, want distinct workspace keys to remain valid", problems)
	}
}

func TestValidate_RegionlessS3LiteralLockAddressWarns(t *testing.T) {
	root := s3PartitionFixture(t, "", "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `lock {
 s3 {
  bucket = "shared-bucket"
  key = "shared.tfstate"
  region = "us-east-1"
 }
}
node "a" { source = "./a" }
`)
	problems := validateLockFixture(t, root)
	if len(problems) != 1 || problems[0].Severity != SeverityWarning || !strings.Contains(problems[0].Message, "graph lock") || !strings.Contains(problems[0].Message, "partition") {
		t.Fatalf("got = %v, want one unverified graph lock/state separation warning", problems)
	}
}

func TestValidate_RegionlessS3ExplicitLockCollisionRemainsError(t *testing.T) {
	root := s3PartitionFixture(t, "", "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `lock {
 s3 {
  bucket = "shared-bucket"
  key = "shared.tfstate"
  region = "us-east-1"
 }
}
node "a" {
 source = "./a"
 backend_config = { key = "shared.tfstate" }
}
`)
	problems := validateLockFixture(t, root)
	if len(problems) != 1 || problems[0].Severity != SeverityError || !hasErrorContaining(problems, "must not be a node's state key") {
		t.Fatalf("got = %v, want legacy explicit lock key error without a duplicate warning", problems)
	}
}
