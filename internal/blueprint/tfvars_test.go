package blueprint

import "testing"

func TestParseFile_NoTFVarsBlockUsesWorkdirDefault(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.TFVars != nil {
		t.Fatalf("expected nil TFVars, got %+v", bp.TFVars)
	}
	if bp.TFVarsLocation() != TFVarsLocationWorkdir {
		t.Fatalf("TFVarsLocation() = %q, want %q", bp.TFVarsLocation(), TFVarsLocationWorkdir)
	}
}

func TestParseFile_TFVarsBlockModuleLocation(t *testing.T) {
	path := writeTemp(t, `
tfvars { location = "module" }
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.TFVarsLocation() != TFVarsLocationModule {
		t.Fatalf("TFVarsLocation() = %q, want %q", bp.TFVarsLocation(), TFVarsLocationModule)
	}
}

func TestParseFile_TFVarsBlockEmptyFallsBackToDefault(t *testing.T) {
	path := writeTemp(t, `
tfvars {}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.TFVarsLocation() != TFVarsLocationWorkdir {
		t.Fatalf("TFVarsLocation() = %q, want %q", bp.TFVarsLocation(), TFVarsLocationWorkdir)
	}
}

func TestParseFile_TFVarsBlockUnknownLocationRejected(t *testing.T) {
	path := writeTemp(t, `
tfvars { location = "elsewhere" }
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for an unknown tfvars location")
	}
}

func TestParseFile_DuplicateTFVarsBlockRejected(t *testing.T) {
	path := writeTemp(t, `
tfvars { location = "workdir" }
tfvars { location = "module" }
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate tfvars block")
	}
}
