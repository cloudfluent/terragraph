package blueprint

import (
	"strings"
	"testing"
)

const lockS3HCL = `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`

func TestParseFile_LockS3(t *testing.T) {
	bp, err := ParseFile(writeTemp(t, lockS3HCL))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Lock == nil || bp.Lock.S3 == nil {
		t.Fatalf("expected lock.s3, got %+v", bp.Lock)
	}
	s3 := bp.Lock.S3
	if s3.Bucket != "acme-tfstate" || s3.Key != "terragraph/prod.lock" || s3.Region != "ap-northeast-2" {
		t.Fatalf("unexpected s3 lock: %+v", s3)
	}
}

func TestParseFile_NoLockBlock(t *testing.T) {
	bp, err := ParseFile(writeTemp(t, `node "a" { source = "./a" }`))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Lock != nil {
		t.Fatalf("expected nil Lock, got %+v", bp.Lock)
	}
}

func TestParseFile_EmptyLockRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `lock {}`))
	if err == nil {
		t.Fatal("expected an error for an empty lock block")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("error = %q, want substring %q", err, "exactly one")
	}
}

func TestParseFile_DuplicateLockRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, lockS3HCL+lockS3HCL))
	if err == nil {
		t.Fatal("expected an error for a duplicate lock block")
	}
	if !strings.Contains(err.Error(), "duplicate lock") {
		t.Fatalf("error = %q, want substring %q", err, "duplicate lock")
	}
}

func TestParseFile_TwoLockBackendsRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
  s3 {
    bucket = "other"
    key    = "other.lock"
    region = "us-east-1"
  }
}
`))
	if err == nil {
		t.Fatal("expected an error for two nested lock backends")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("error = %q, want substring %q", err, "exactly one")
	}
}

func TestParseFile_UnknownLockBackendRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `
lock {
  dynamodb {
    table = "t"
    key   = "k"
  }
}
`))
	if err == nil {
		t.Fatal("expected an error for an unknown lock backend")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("error = %q, want substring %q", err, "unknown backend")
	}
}

func TestParseFile_LockS3AndUnknownBackendRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
  dynamodb {
    table = "t"
    key   = "k"
  }
}
`))
	if err == nil {
		t.Fatal("expected an error for s3 and dynamodb together")
	}
}

func TestParseFile_LockS3MissingBucketRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `
lock {
  s3 {
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`))
	if err == nil {
		t.Fatal("expected an error for s3 without bucket")
	}
}

func TestParseFile_LockS3EmptyKeyRejected(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = ""
    region = "ap-northeast-2"
  }
}
`))
	if err == nil {
		t.Fatal("expected an error for an empty lock key")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Fatalf("error = %q, want substring %q", err, "key")
	}
}
