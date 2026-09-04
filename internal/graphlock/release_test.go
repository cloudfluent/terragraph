package graphlock

import (
	"context"
	"testing"
)

func TestRelease_S3DeletesConfiguredObject(t *testing.T) {
	mem := newMemS3()
	mem.objects["acme-tfstate/terragraph/prod.lock"] = memObj{etag: `"stale"`, body: []byte(`{}`)}
	withBackends(t, s3With(mem))
	if err := Release(context.Background(), testS3Lock()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("expected lock object deleted after Release")
	}
}

func TestRelease_NilLockIsNoop(t *testing.T) {
	calls := 0
	withBackends(t, fakeBackend{matches: true, calls: &calls})
	if err := Release(context.Background(), nil); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if calls != 0 {
		t.Fatalf("backend called %d times, want 0", calls)
	}
}
