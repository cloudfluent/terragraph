package blueprint

import "testing"

func TestIsRemote(t *testing.T) {
	cases := map[string]bool{
		"./stacks/vpc":                         false,
		"../modules/vpc":                       false,
		"git::https://github.com/x/y.git":      true,
		"github.com/terraform-aws-modules/vpc": true,
	}
	for src, want := range cases {
		if got := IsRemote(src); got != want {
			t.Errorf("IsRemote(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestParseFile_VendorBlockAllFields(t *testing.T) {
	path := writeTemp(t, `
vendor {
  directory     = "third_party"
  manifest_file = "third_party.yaml"
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.VendorDirectory() != "third_party" {
		t.Fatalf("VendorDirectory() = %q, want %q", bp.VendorDirectory(), "third_party")
	}
	if bp.VendorManifestFile() != "third_party.yaml" {
		t.Fatalf("VendorManifestFile() = %q, want %q", bp.VendorManifestFile(), "third_party.yaml")
	}
}

func TestParseFile_NoVendorBlockUsesDefaults(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Vendor != nil {
		t.Fatalf("expected nil Vendor, got %+v", bp.Vendor)
	}
	if bp.VendorDirectory() != DefaultVendorDirectory {
		t.Fatalf("VendorDirectory() = %q, want default %q", bp.VendorDirectory(), DefaultVendorDirectory)
	}
	if bp.VendorManifestFile() != DefaultVendorManifestFile {
		t.Fatalf("VendorManifestFile() = %q, want default %q", bp.VendorManifestFile(), DefaultVendorManifestFile)
	}
}

func TestParseFile_VendorBlockPartialFieldsFallBackToDefaults(t *testing.T) {
	path := writeTemp(t, `
vendor {
  directory = "third_party"
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.VendorDirectory() != "third_party" {
		t.Fatalf("VendorDirectory() = %q, want %q", bp.VendorDirectory(), "third_party")
	}
	if bp.VendorManifestFile() != DefaultVendorManifestFile {
		t.Fatalf("VendorManifestFile() = %q, want default %q", bp.VendorManifestFile(), DefaultVendorManifestFile)
	}
}

func TestParseFile_DuplicateVendorBlock(t *testing.T) {
	path := writeTemp(t, `
vendor { directory = "a" }
vendor { directory = "b" }
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate vendor block")
	}
}
