package graphlock

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
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
	if !strings.Contains(err.Error(), "held by") {
		t.Fatalf("second Acquire: err = %q, want holder details", err)
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

func TestS3AcquireNoETagFailsAndDoesNotLeak(t *testing.T) {
	mem := newMemS3()
	b := s3Backend{newClient: func(ctx context.Context, region string) (s3API, error) {
		return &noETagS3{inner: mem}, nil
	}}
	_, err := b.Acquire(context.Background(), testS3Lock())
	if err == nil || !strings.Contains(err.Error(), "no ETag") {
		t.Fatalf("Acquire: err = %v, want no ETag", err)
	}
	if mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("missing-ETag Acquire must not leave the lock object")
	}
}

func TestS3AcquireConflictIsHeld(t *testing.T) {
	b := s3Backend{newClient: func(ctx context.Context, region string) (s3API, error) {
		return &conflictS3{}, nil
	}}
	_, err := b.Acquire(context.Background(), testS3Lock())
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("Acquire: err = %v, want ErrHeld", err)
	}
}

func TestS3CloseRetriesAfterDeleteFailure(t *testing.T) {
	mem := newMemS3()
	b := s3With(mem)
	held, err := b.Acquire(context.Background(), testS3Lock())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	mem.setFailDelete(errors.New("403"))
	if err := held.Close(); err == nil {
		t.Fatal("Close should surface DeleteObject failure")
	}
	if !mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("failed Close must leave the lock held")
	}
	mem.setFailDelete(nil)
	if err := held.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	if mem.has("acme-tfstate", "terragraph/prod.lock") {
		t.Fatal("retry Close should delete the lock object")
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
	if !isHeld(&conditionalConflictError{}) {
		t.Fatal("ConditionalRequestConflict API error should be held")
	}
	if !isHeld(&statusError{status: http.StatusConflict}) {
		t.Fatal("HTTP 409 should be held")
	}
	if isHeld(errors.New("no such bucket")) {
		t.Fatal("unrelated error should not be held")
	}
}

func TestMemS3PutWithoutIfNoneMatchOverwrites(t *testing.T) {
	mem := newMemS3()
	first, err := mem.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("b"),
		Key:    aws.String("k"),
	})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := mem.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("b"),
		Key:    aws.String("k"),
	})
	if err != nil {
		t.Fatalf("unconditional Put should overwrite, not contend: %v", err)
	}
	if aws.ToString(first.ETag) == aws.ToString(second.ETag) {
		t.Fatal("overwrite should mint a new ETag")
	}
}

// memS3 is an in-memory S3 API. A second Put is PreconditionFailed only when
// IfNoneMatch is "*"; otherwise it overwrites.
type memS3 struct {
	mu         sync.Mutex
	objects    map[string]memObj
	seq        int
	failDelete error
}

type memObj struct {
	etag string
	body []byte
}

func newMemS3() *memS3 {
	return &memS3{objects: map[string]memObj{}}
}

func (m *memS3) id(bucket, key string) string { return bucket + "/" + key }

func (m *memS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.id(aws.ToString(params.Bucket), aws.ToString(params.Key))
	if _, ok := m.objects[id]; ok && aws.ToString(params.IfNoneMatch) == "*" {
		return nil, &preconditionFailedError{}
	}
	var body []byte
	if params.Body != nil {
		var err error
		body, err = io.ReadAll(params.Body)
		if err != nil {
			return nil, err
		}
	}
	m.seq++
	etag := `"` + strconv.Itoa(m.seq) + `"`
	m.objects[id] = memObj{etag: etag, body: body}
	return &s3.PutObjectOutput{ETag: aws.String(etag)}, nil
}

func (m *memS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.id(aws.ToString(params.Bucket), aws.ToString(params.Key))
	obj, ok := m.objects[id]
	if !ok {
		return nil, &statusError{status: http.StatusNotFound}
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(obj.body)),
		ETag: aws.String(obj.etag),
	}, nil
}

func (m *memS3) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDelete != nil {
		return nil, m.failDelete
	}
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

func (m *memS3) setFailDelete(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failDelete = err
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

type conditionalConflictError struct{}

func (e *conditionalConflictError) Error() string {
	return "ConditionalRequestConflict: A conflicting conditional operation is currently in progress against this resource"
}
func (e *conditionalConflictError) ErrorCode() string { return "ConditionalRequestConflict" }
func (e *conditionalConflictError) ErrorMessage() string {
	return "A conflicting conditional operation is currently in progress against this resource"
}
func (e *conditionalConflictError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

type statusError struct{ status int }

func (e *statusError) Error() string       { return http.StatusText(e.status) }
func (e *statusError) HTTPStatusCode() int { return e.status }

type noETagS3 struct{ inner *memS3 }

func (n *noETagS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	out, err := n.inner.PutObject(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	out.ETag = nil
	return out, nil
}
func (n *noETagS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return n.inner.GetObject(ctx, params, optFns...)
}
func (n *noETagS3) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return n.inner.DeleteObject(ctx, params, optFns...)
}

type conflictS3 struct{}

func (c *conflictS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return nil, &conditionalConflictError{}
}
func (c *conflictS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return nil, &statusError{status: http.StatusNotFound}
}
func (c *conflictS3) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return &s3.DeleteObjectOutput{}, nil
}

var (
	_ smithy.APIError = (*preconditionFailedError)(nil)
	_ smithy.APIError = (*conditionalConflictError)(nil)
	_ s3API           = (*memS3)(nil)
	_ s3API           = (*noETagS3)(nil)
	_ s3API           = (*conflictS3)(nil)
)
