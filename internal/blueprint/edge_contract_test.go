package blueprint

import (
	"strings"
	"testing"
)

const completeEdgeContract = `
  contract {
    producer {
      type      = "string"
      nullable  = false
      sensitive = true
    }
    consumer {
      type      = "string"
      nullable  = false
      sensitive = true
    }
  }
`

func TestParseFile_DataEdgeContract(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a.output.id
  to   = node.b.input.id
`+completeEdgeContract+`}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	contract := bp.Edges[0].Contract
	if contract == nil {
		t.Fatal("expected edge contract")
	}
	if contract.Producer.Type != "string" || contract.Producer.Nullable || !contract.Producer.Sensitive {
		t.Fatalf("unexpected producer contract: %+v", contract.Producer)
	}
	if contract.Consumer.Type != "string" || contract.Consumer.Nullable || !contract.Consumer.Sensitive {
		t.Fatalf("unexpected consumer contract: %+v", contract.Consumer)
	}
}

func TestParseFile_NestedInputContract(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a
  to   = node.b
  input "id" {
    from = output.id
`+completeEdgeContract+`  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Edges) != 1 || bp.Edges[0].Contract == nil {
		t.Fatalf("expected expanded edge contract, got %+v", bp.Edges)
	}
}

func TestParseFile_OrderingEdgeContractRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a
  to   = node.b
`+completeEdgeContract+`}
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "data edge") {
		t.Fatalf("error = %v, want data-edge contract rejection", err)
	}
}

func TestParseFile_EdgeContractRequiresBothRoles(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a.output.id
  to   = node.b.input.id
  contract {
    producer {
      type      = "string"
      nullable  = false
      sensitive = false
    }
  }
}
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "consumer") {
		t.Fatalf("error = %v, want missing consumer error", err)
	}
}

func TestParseFile_EdgeContractRequiresEveryField(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a.output.id
  to   = node.b.input.id
  contract {
    producer {
      type     = "string"
      nullable = false
    }
    consumer {
      type      = "string"
      nullable  = false
      sensitive = false
    }
  }
}
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("error = %v, want missing sensitive error", err)
	}
}

func TestParseFile_EdgeContractRejectsInvalidTypeConstraint(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a.output.id
  to   = node.b.input.id
  contract {
    producer {
      type      = "not_a_terraform_type"
      nullable  = false
      sensitive = false
    }
    consumer {
      type      = "string"
      nullable  = false
      sensitive = false
    }
  }
}
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "type") || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("error = %v, want invalid type constraint error", err)
	}
}

func TestParseFile_EdgeContractRejectsDuplicateRole(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a.output.id
  to   = node.b.input.id
  contract {
    producer {
      type      = "string"
      nullable  = false
      sensitive = false
    }
    producer {
      type      = "string"
      nullable  = false
      sensitive = false
    }
    consumer {
      type      = "string"
      nullable  = false
      sensitive = false
    }
  }
}
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "exactly one producer") {
		t.Fatalf("error = %v, want duplicate producer error", err)
	}
}

func TestParseFile_ParentContractWithInputBlocksRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
edge {
  from = node.a
  to   = node.b
`+completeEdgeContract+`
  input "id" { from = output.id }
}
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "inside the input block") {
		t.Fatalf("error = %v, want per-input contract error", err)
	}
}
