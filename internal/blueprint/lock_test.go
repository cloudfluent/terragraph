package blueprint

import (
	"strings"
	"testing"
)

func TestParseFile_LockS3(t *testing.T) {
	path := writeTemp(t, `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Lock == nil || bp.Lock.S3 == nil {
		t.Fatalf("expected lock.s3, got %+v", bp.Lock)
	}
	if bp.Lock.S3.Bucket != "acme-tfstate" || bp.Lock.S3.Key != "terragraph/prod.lock" || bp.Lock.S3.Region != "ap-northeast-2" {
		t.Fatalf("unexpected s3 config: %+v", bp.Lock.S3)
	}
}

func TestParseFile_NoLockIsNil(t *testing.T) {
	path := writeTemp(t, `node "a" { source = "./a" }`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Lock != nil {
		t.Fatalf("expected nil Lock, got %+v", bp.Lock)
	}
}

func TestParseFile_EmptyLock(t *testing.T) {
	path := writeTemp(t, `lock {}`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "exactly one nested backend") {
		t.Fatalf("err = %v, want empty lock error", err)
	}
}

func TestParseFile_LockS3AndDynamo(t *testing.T) {
	path := writeTemp(t, `
lock {
  s3 {
    bucket = "b"
    key    = "k"
    region = "r"
  }
  dynamodb {
    table = "t"
    key   = "k"
  }
}
`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "exactly one nested backend") {
		t.Fatalf("err = %v, want multiple backend error", err)
	}
}

func TestParseFile_LockDynamoNotImplemented(t *testing.T) {
	path := writeTemp(t, `
lock {
  dynamodb {
    table = "t"
    key   = "k"
  }
}
`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("err = %v, want not implemented", err)
	}
}

func TestParseFile_DuplicateLock(t *testing.T) {
	path := writeTemp(t, `
lock {
  s3 {
    bucket = "b"
    key    = "k"
    region = "r"
  }
}
lock {
  s3 {
    bucket = "b2"
    key    = "k2"
    region = "r2"
  }
}
`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate lock") {
		t.Fatalf("err = %v, want duplicate lock", err)
	}
}

func TestParseFile_LockS3MissingField(t *testing.T) {
	path := writeTemp(t, `
lock {
  s3 {
    bucket = "b"
    key    = "k"
  }
}
`)
	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected missing region error")
	}
}
