package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/marcus/td/internal/db"
	"github.com/marcus/td/internal/serve"
	"github.com/spf13/cobra"
)

func TestServeLifecycle(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Initialize(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	previousOverride := baseDirOverride
	baseDirOverride = &dir
	t.Cleanup(func() { baseDirOverride = previousOverride })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := &cobra.Command{Use: "serve"}
	cmd.SetContext(ctx)
	cmd.Flags().Int("port", 0, "")
	cmd.Flags().String("addr", "127.0.0.1", "")
	cmd.Flags().String("token", "", "")
	cmd.Flags().String("cors", "", "")
	cmd.Flags().Duration("interval", 50*time.Millisecond, "")
	done := make(chan error, 1)
	go func() { done <- runServe(cmd, nil) }()
	var info *serve.PortInfo
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err = serve.ReadPortFile(dir)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server did not start: %v", err)
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/project", info.Port))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "issue_revisions") {
		t.Fatalf("server page: %d %s", resp.StatusCode, page)
	}
	stream, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/events", info.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Body.Close() }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop with an SSE client connected")
	}
	if _, err := serve.ReadPortFile(dir); err == nil {
		t.Fatal("server left its port file behind")
	}
	if _, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/project", info.Port)); err == nil {
		t.Fatal("server listener still running")
	}
}
