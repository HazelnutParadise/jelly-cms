package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3ContractObjectLifecycleAgainstIsolatedS3Endpoint(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	requests := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			http.Error(w, "missing AWS Signature V4 authorization", http.StatusForbidden)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/integration-bucket/")
		mu.Lock()
		defer mu.Unlock()
		requests[r.Method]++
		if r.Method == http.MethodHead && r.URL.Path == "/integration-bucket" {
			w.WriteHeader(http.StatusOK)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read body", http.StatusBadRequest)
				return
			}
			objects[key] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			body, ok := objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		case http.MethodHead:
			body, ok := objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		case http.MethodDelete:
			delete(objects, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unsupported S3 operation", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "codex-local-test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "codex-local-test-secret")
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	d := NewS3(srv.URL, "integration-bucket")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Test(ctx); err != nil {
		t.Fatalf("Test S3 driver: %v", err)
	}
	key := fmt.Sprintf("codex-contract/%d/object.txt", time.Now().UnixNano())
	const content = "S3 contract payload"
	if err := d.Put(ctx, key, strings.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, err := d.Stat(ctx, key)
	if err != nil || info.Size != int64(len(content)) {
		t.Fatalf("Stat = %+v, %v", info, err)
	}
	r, err := d.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, []byte(content)) {
		t.Fatalf("Open payload = %q, read %v, close %v", got, readErr, closeErr)
	}
	if err := d.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := d.Stat(ctx, key); err == nil {
		t.Error("Stat after Delete succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodHead, http.MethodDelete} {
		if requests[method] == 0 {
			t.Errorf("S3 contract never observed signed %s request", method)
		}
	}
}
