package filterliststore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestModeForceFetchIgnoresFreshCache pins the manual-refresh semantics:
// ModeDefault never touches the network behind a fresh entry, while
// ModeForceFetch revalidates it unconditionally.
func TestModeForceFetchIgnoresFreshCache(t *testing.T) {
	var hits int
	body := "! Title: t\n||example.com^\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	store, err := New(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Seed a fresh entry.
	rc, src, err := store.Get(ctx, server.URL, ModeDefault)
	if err != nil || src != SourceNetwork {
		t.Fatalf("seed: src=%v err=%v", src, err)
	}
	_, _ = io.Copy(io.Discard, rc)
	rc.Close()
	if hits != 1 {
		t.Fatalf("seed hits = %d, want 1", hits)
	}

	// ModeDefault on a fresh entry must not touch the network.
	rc, src, err = store.Get(ctx, server.URL, ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, rc)
	rc.Close()
	if src != SourceCache {
		t.Fatalf("default-mode source = %v, want SourceCache", src)
	}
	if hits != 1 {
		t.Fatalf("default mode hit the network: hits = %d, want 1", hits)
	}

	// ModeForceFetch must revalidate the fresh entry over the network.
	rc, src, err = store.Get(ctx, server.URL, ModeForceFetch)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, rc)
	rc.Close()
	if src != SourceNetwork {
		t.Fatalf("force-mode source = %v, want SourceNetwork", src)
	}
	if hits != 2 {
		t.Fatalf("force hits = %d, want 2", hits)
	}
}
