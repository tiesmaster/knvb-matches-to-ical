package store

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestLocal(t *testing.T) {
	ctx := context.Background()
	l := Local{Dir: t.TempDir()}
	if _, found, err := l.Get(ctx, "abc/state.json"); found || err != nil {
		t.Fatalf("missing object: found=%v err=%v", found, err)
	}
	if err := l.Put(ctx, "abc/state.json", []byte("{}"), "application/json", ""); err != nil {
		t.Fatal(err)
	}
	b, found, err := l.Get(ctx, "abc/state.json")
	if !found || err != nil || string(b) != "{}" {
		t.Fatalf("got %q %v %v", b, found, err)
	}
}

// fakeS3 is a tiny path-style S3 endpoint.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	headers map[string]http.Header
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-key/") {
		http.Error(w, "unsigned", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		b, ok := f.objects[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`)
			return
		}
		w.Write(b)
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = b
		f.headers[r.URL.Path] = r.Header.Clone()
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

func TestR2(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, headers: map[string]http.Header{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r := NewR2(R2Config{AccessKeyID: "test-key", SecretAccessKey: "test-secret", Bucket: "cal", Endpoint: srv.URL})
	ctx := context.Background()

	if _, found, err := r.Get(ctx, "x7f3k/state.json"); found || err != nil {
		t.Fatalf("missing object: found=%v err=%v", found, err)
	}
	if err := r.Put(ctx, "x7f3k/o8-1.ics", []byte("BEGIN:VCALENDAR"), "text/calendar; charset=utf-8", "max-age=300"); err != nil {
		t.Fatal(err)
	}
	h := fake.headers["/cal/x7f3k/o8-1.ics"]
	if h.Get("Content-Type") != "text/calendar; charset=utf-8" || h.Get("Cache-Control") != "max-age=300" {
		t.Errorf("headers: %v", h)
	}
	b, found, err := r.Get(ctx, "x7f3k/o8-1.ics")
	if !found || err != nil || string(b) != "BEGIN:VCALENDAR" {
		t.Fatalf("got %q %v %v", b, found, err)
	}
}
