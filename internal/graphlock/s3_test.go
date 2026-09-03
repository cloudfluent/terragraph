package graphlock

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func testS3Lock() *blueprint.Lock {
	return &blueprint.Lock{S3: &blueprint.S3Lock{
		Bucket: "acme-tfstate",
		Key:    "terragraph/prod.lock",
		Region: "ap-northeast-2",
	}}
}

func s3With(mem *memS3) s3Backend {
	return s3Backend{newClient: func(ctx context.Context, region string) (s3API, error) {
		return mem, nil
	}}
}

func TestS3Matches(t *testing.T) {
	var b s3Backend
	if b.Matches(nil) || b.Matches(&blueprint.Lock{}) {
		t.Fatal("Matches should be false without s3")
	}
	if !b.Matches(testS3Lock()) {
		t.Fatal("Matches should be true with s3")
	}
}

func TestS3AcquireRelease(t *testing.T) {
	mem := newMemS3()
	b := s3With(mem)
	held, err := b.Acquire(context.Background(), testS3Lock())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("expected lock object after Acquire")
	}
	if err := held.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("expected lock object deleted after Close")
	}

	held, err = b.Acquire(context.Background(), testS3Lock())
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestS3AcquireContention(t *testing.T) {
	mem := newMemS3()
	b := s3With(mem)
	first, err := b.Acquire(context.Background(), testS3Lock())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	_, err = b.Acquire(context.Background(), testS3Lock())
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire: err = %v, want ErrHeld", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	third, err := b.Acquire(context.Background(), testS3Lock())
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("third Close: %v", err)
	}
}

func TestS3ReleaseDoesNotDeleteStolenLock(t *testing.T) {
	mem := newMemS3()
	b := s3With(mem)
	held, err := b.Acquire(context.Background(), testS3Lock())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	mem.setETag("acme-tfstate", "terragraph/prod.lock", `"stolen"`)
	if err := held.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("Close must not delete a lock whose etag no longer matches")
	}
}

func TestIsHeld_PreconditionFailedCode(t *testing.T) {
	if !isHeld(&preconditionFailedError{}) {
		t.Fatal("PreconditionFailed API error should be held")
	}
	if !isHeld(&statusError{status: http.StatusPreconditionFailed}) {
		t.Fatal("HTTP 412 should be held")
	}
	if isHeld(errors.New("no such bucket")) {
		t.Fatal("unrelated error should not be held")
	}
}

// memS3 is an in-memory S3 API: first Put wins; a second Put is PreconditionFailed.
type memS3 struct {
	mu      sync.Mutex
	objects map[string]memObj
	seq     int
}

type memObj struct {
	etag string
}

func newMemS3() *memS3 {
	return &memS3{objects: map[string]memObj{}}
}

func (m *memS3) id(bucket, key string) string { return bucket + "/" + key }

func (m *memS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.id(aws.ToString(params.Bucket), aws.ToString(params.Key))
	if _, ok := m.objects[id]; ok {
		return nil, &preconditionFailedError{}
	}
	if params.Body != nil {
		_, _ = io.Copy(io.Discard, params.Body)
	}
	m.seq++
	etag := `"` + strconv.Itoa(m.seq) + `"`
	m.objects[id] = memObj{etag: etag}
	return &s3.PutObjectOutput{ETag: aws.String(etag)}, nil
}

func (m *memS3) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.id(aws.ToString(params.Bucket), aws.ToString(params.Key))
	obj, ok := m.objects[id]
	if !ok {
		return &s3.DeleteObjectOutput{}, nil
	}
	if params.IfMatch != nil && obj.etag != *params.IfMatch {
		return nil, &preconditionFailedError{}
	}
	delete(m.objects, id)
	return &s3.DeleteObjectOutput{}, nil
}

func (m *memS3) has(bucket, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[m.id(bucket, key)]
	return ok
}

func (m *memS3) setETag(bucket, key, etag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.id(bucket, key)
	obj := m.objects[id]
	obj.etag = etag
	m.objects[id] = obj
}

// preconditionFailedError implements smithy.APIError the way AWS S3 reports If-None-Match failure.
type preconditionFailedError struct{}

func (e *preconditionFailedError) Error() string {
	return "PreconditionFailed: At least one of the pre-conditions you specified did not hold"
}
func (e *preconditionFailedError) ErrorCode() string { return "PreconditionFailed" }
func (e *preconditionFailedError) ErrorMessage() string {
	return "At least one of the pre-conditions you specified did not hold"
}
func (e *preconditionFailedError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

type statusError struct{ status int }

func (e *statusError) Error() string       { return http.StatusText(e.status) }
func (e *statusError) HTTPStatusCode() int { return e.status }

var (
	_ smithy.APIError = (*preconditionFailedError)(nil)
	_ s3API           = (*memS3)(nil)
)
