package blueprint

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseFile_TypedNullSourceRejected(t *testing.T) {
	path := writeTemp(t, `node "a" { source = true ? null : "./module" }`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "source") || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want source error with file location", err)
	}
}

func TestParseFile_TypedNullAttributesRejected(t *testing.T) {
	for _, tc := range []struct {
		name, text, attribute string
	}{
		{"vendor directory", `vendor { directory = true ? null : "vendor" }`, "directory"},
		{"vendor manifest", `vendor { manifest_file = true ? null : "vendor.yaml" }`, "manifest_file"},
		{"tfvars location", `tfvars { location = true ? null : "workdir" }`, "location"},
		{"contracts mode", `contracts { mode = true ? null : "warn" }`, "mode"},
		{"runtime binary", `runtime "tf" { binary = true ? null : "terraform" }`, "binary"},
		{"runtime version", "runtime \"tf\" {\n binary = \"terraform\"\n version = true ? null : \"1.5.7\"\n}", "version"},
		{"node approve", "node \"a\" {\n source = \"./a\"\n approve = true ? null : \"safe\"\n}", "approve"},
		{"node vars", "node \"a\" {\n source = \"./a\"\n vars = true ? null : { x = 1 }\n}", "vars"},
		{"use name", "use \"g\" {\n as = true ? null : \"inst\"\n source = \"./g\"\n}", "as"},
		{"use source", "use \"g\" {\n as = \"inst\"\n source = true ? null : \"./g\"\n}", "source"},
		{"lock bucket", "lock {\n s3 {\n bucket = true ? null : \"bucket\"\n key = \"lock\"\n region = \"us-east-1\"\n }\n}", "bucket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, tc.text)
			_, err := ParseFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.attribute) || !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %v, want %s error with file location", err, tc.attribute)
			}
		})
	}
}

func TestParseFile_StringMapAttributesRejectNonObjects(t *testing.T) {
	for _, block := range []string{"node", "use"} {
		for _, attr := range []string{"env", "backend_config"} {
			for _, value := range []string{`[]`, `["value"]`, `true ? null : { key = "value" }`} {
				t.Run(block+"/"+attr+"/"+value, func(t *testing.T) {
					body := fmt.Sprintf("%s \"a\" {\n source = \"./a\"\n %s = %s\n", block, attr, value)
					if block == "use" {
						body += "as = \"inst\"\n"
					}
					path := writeTemp(t, body+"}")
					_, err := ParseFile(path)
					if err == nil || !strings.Contains(err.Error(), attr) || !strings.Contains(err.Error(), path) {
						t.Fatalf("error = %v, want %s error with file location", err, attr)
					}
				})
			}
		}
	}
}
