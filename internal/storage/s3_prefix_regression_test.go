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

func TestS3PrefixRegressionTestObjectUsesConfiguredPrefix(t *testing.T) {
	const bucket = "integration-bucket"
	const prefix = "private/backups"
	var mu sync.Mutex
	objects := map[string][]byte{}
	var operationKeys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			http.Error(w, "missing AWS Signature V4 authorization", http.StatusForbidden)
			return
		}
		bucketPath := "/" + bucket
		if r.Method == http.MethodHead && r.URL.Path == bucketPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, bucketPath+"/")
		if key == r.URL.Path || !strings.HasPrefix(key, prefix+"/") {
			http.Error(w, "object is outside the credential prefix", http.StatusForbidden)
			return
		}

		mu.Lock()
		defer mu.Unlock()
		operationKeys = append(operationKeys, r.Method+" "+key)
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
	t.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	d, err := NewS3WithConfig(S3Config{
		Endpoint:  srv.URL,
		Bucket:    bucket,
		Region:    "us-east-1",
		Prefix:    prefix,
		AccessKey: "codex-local-test",
		SecretKey: "codex-local-test-secret",
	})
	if err != nil {
		t.Fatalf("NewS3WithConfig: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Prove the configured prefix already scopes ordinary storage operations.
	trialPath := "prefix-regression/" + fmt.Sprint(time.Now().UTC().UnixNano()) + ".txt"
	const content = "prefix-scoped S3 payload"
	if err := d.Put(ctx, trialPath, strings.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("Put under configured prefix: %v", err)
	}
	r, err := d.Open(ctx, trialPath)
	if err != nil {
		t.Fatalf("Open under configured prefix: %v", err)
	}
	got, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, []byte(content)) {
		t.Fatalf("Open payload = %q, read %v, close %v", got, readErr, closeErr)
	}
	if err := d.Delete(ctx, trialPath); err != nil {
		t.Fatalf("Delete under configured prefix: %v", err)
	}

	mu.Lock()
	if len(objects) != 0 {
		mu.Unlock()
		t.Fatalf("trial object remained after Delete: %v", objects)
	}
	for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodHead, http.MethodDelete} {
		found := false
		for _, operation := range operationKeys {
			if strings.HasPrefix(operation, method+" "+prefix+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ordinary S3 operations never observed prefixed %s", method)
		}
	}
	mu.Unlock()

	// Test performs its own signed write/read/delete probe. Prefix-only
	// credentials must be able to run it and the probe must leave no object.
	if err := d.Test(ctx); err != nil {
		t.Fatalf("Test with prefix-only S3 permissions: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(objects) != 0 {
		t.Errorf("S3 Test left objects behind: %v", objects)
	}
	testObject := ""
	testOperations := make(map[string]string)
	for _, operation := range operationKeys {
		method, key, ok := strings.Cut(operation, " ")
		if !ok {
			t.Errorf("malformed recorded S3 operation %q", operation)
			continue
		}
		if strings.HasPrefix(key, ".jelly-test-") {
			t.Errorf("S3 Test escaped configured prefix: %s", operation)
		}
		if strings.HasPrefix(key, prefix+"/.jelly-test-") {
			if testObject != "" && testObject != key {
				t.Errorf("S3 Test used different probe object keys: %q and %q", testObject, key)
			}
			testObject = key
			testOperations[method] = key
		}
	}
	if testObject == "" {
		t.Error("S3 Test did not use a probe object under the configured prefix")
	}
	for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
		if got := testOperations[method]; got != testObject || got == "" {
			t.Errorf("S3 Test %s probe key = %q, want %q", method, got, testObject)
		}
	}
}
