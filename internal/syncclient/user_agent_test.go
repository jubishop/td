package syncclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRequestsSendUserAgent covers each place the client builds a request:
// the JSON helper, the snapshot download, and the event stream.
func TestRequestsSendUserAgent(t *testing.T) {
	oldUserAgent := userAgent
	t.Cleanup(func() { userAgent = oldUserAgent })
	SetVersion("v1.2.3")
	want := fmt.Sprintf("td/v1.2.3 (%s/%s)", runtime.GOOS, runtime.GOARCH)

	var mu sync.Mutex
	got := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = r.UserAgent()
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/snapshot"):
			w.Header().Set("X-Snapshot-Seq", "1")
			_, _ = w.Write([]byte("db"))
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	client := New(server.URL, "key", "device")
	if _, err := client.HealthCheck(); err != nil {
		t.Fatalf("health check: %v", err)
	}
	if _, err := client.GetSnapshot("p"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	_ = client.Events(context.Background(), "p", "", time.Second, nil, nil)

	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/healthz", "/v1/projects/p/sync/snapshot", "/v1/projects/p/events"} {
		if got[path] != want {
			t.Errorf("%s User-Agent = %q, want %q", path, got[path], want)
		}
	}
}
