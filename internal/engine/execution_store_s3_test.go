package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func s3StoreFixture(t *testing.T) *s3ExecutionStore {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	etags := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fail := func(code string, status int) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code></Error>", code)
		}
		key := strings.TrimPrefix(r.URL.Path, "/bucket/")
		if r.URL.Query().Get("list-type") == "2" {
			names := []string{}
			for name := range objects {
				if strings.HasPrefix(name, r.URL.Query().Get("prefix")) {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			_, _ = io.WriteString(w, "<ListBucketResult><IsTruncated>false</IsTruncated>")
			for _, name := range names {
				_, _ = fmt.Fprintf(w, "<Contents><Key>%s</Key></Contents>", name)
			}
			_, _ = io.WriteString(w, "</ListBucketResult>")
			return
		}
		switch r.Method {
		case http.MethodPut:
			match, create := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
			if match == "" && create != "*" {
				fail("InvalidRequest", 400)
				return
			}
			if (create == "*" && etags[key] != "") || (match != "" && match != etags[key]) {
				fail("PreconditionFailed", 412)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				fail("InternalError", 500)
				return
			}
			digest := sha256.Sum256(data)
			etags[key] = "\"" + hex.EncodeToString(digest[:]) + "\""
			objects[key] = data
			w.Header().Set("ETag", etags[key])
			w.WriteHeader(200)
		case http.MethodGet:
			data, ok := objects[key]
			if !ok {
				fail("NoSuchKey", 404)
				return
			}
			w.Header().Set("ETag", etags[key])
			_, _ = w.Write(data)
		case http.MethodDelete:
			if r.Header.Get("If-Match") == "" || r.Header.Get("If-Match") != etags[key] {
				fail("PreconditionFailed", 412)
				return
			}
			delete(objects, key)
			delete(etags, key)
			w.WriteHeader(204)
		default:
			fail("InvalidRequest", 400)
		}
	}))
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region: "us-east-1", HTTPClient: server.Client(), RetryMaxAttempts: 1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "fixture", SecretAccessKey: "fixture"}, nil
		}),
	}, func(o *s3.Options) { o.BaseEndpoint = aws.String(server.URL); o.UsePathStyle = true })
	return &s3ExecutionStore{client: client, bucket: "bucket", prefix: "executions/"}
}

func TestS3ExecutionStore_ConditionalWritesPreserveStartedAttempt(t *testing.T) {
	s := s3StoreFixture(t)
	ctx := context.Background()
	key := newExecutionID("run") + ".json"
	if _, err := s.read(ctx, key); !errors.Is(err, errExecutionMissing) {
		t.Fatalf("got = %v", err)
	}
	first, err := s.write(ctx, key, []byte("prepared"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.write(ctx, key, []byte("overwrite"), ""); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v", err)
	}
	second, err := s.write(ctx, key, []byte("started"), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.write(ctx, key, []byte("success"), first); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v", err)
	}
	if err := s.remove(ctx, key, first); !errors.Is(err, errExecutionConflict) {
		t.Fatalf("got = %v", err)
	}
	obj, err := s.read(ctx, key)
	if err != nil || string(obj.Data) != "started" || obj.Revision != second {
		t.Fatalf("got = %+v, %v", obj, err)
	}
	keys, err := s.list(ctx)
	if err != nil || len(keys) != 1 || keys[0] != key {
		t.Fatalf("got = %v, %v", keys, err)
	}
	if err := s.remove(ctx, key, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(ctx, key); !errors.Is(err, errExecutionMissing) {
		t.Fatalf("got = %v", err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
}

func TestS3ExecutionStore_RejectsIncompleteLocation(t *testing.T) {
	if _, err := openS3ExecutionStore(context.Background(), "bucket", "/", ""); err == nil {
		t.Fatal("accepted incomplete location")
	}
}
