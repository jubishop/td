package e2e

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	bin := filepath.Join(t.TempDir(), "td-sync")
	if out, err := runCmd(findRepoRoot(), "go", "build", "-o", bin, "./cmd/td-sync"); err != nil {
		t.Fatalf("build td-sync: %v\n%s", err, out)
	}

	t.Run("occupied address cannot pass health check", func(t *testing.T) {
		occupant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer occupant.Close()
		port := occupant.Listener.Addr().(*net.TCPAddr).Port
		h := startupHarness(t, bin, port)
		started := time.Now()
		if err := h.StartServer(); err == nil {
			t.Fatal("startup accepted another server's health response on the occupied address")
		} else if !strings.Contains(err.Error(), "address already in use") {
			t.Fatalf("expected the child bind failure, got: %v", err)
		}
		if h.serverCmd != nil {
			t.Fatal("failed startup retained the child process")
		}
		if time.Since(started) > 5*time.Second {
			t.Fatal("child exit was not detected promptly")
		}
	})

	t.Run("retry collision then restart on the same address", func(t *testing.T) {
		port, occupant := occupySelectedPort(t)
		defer func() { _ = occupant.Close() }()
		h := startupHarness(t, bin, port)
		attempts := 0
		err := h.startServerWithRetry(func() (int, error) {
			attempts++
			if h.serverCmd != nil {
				t.Fatal("previous child was not cleaned up before retry")
			}
			if attempts == 1 {
				return port, nil
			}
			return randomPort()
		})
		if err != nil {
			t.Fatalf("startup did not recover from collision: %v", err)
		}
		if attempts < 2 || h.serverPort == port {
			t.Fatalf("expected a new port after collision: attempts=%d port=%d", attempts, h.serverPort)
		}
		url := h.ServerURL
		cmd, done := h.serverCmd, h.serverDone
		if err := h.StartServer(); err == nil || h.serverCmd != cmd {
			t.Fatal("double start must preserve the running child and return an error")
		}
		if err := h.StopServer(); err != nil {
			t.Fatalf("stop server: %v", err)
		}
		select {
		case <-done:
			if cmd.ProcessState == nil {
				t.Fatal("stopped child was not reaped")
			}
		default:
			t.Fatal("stop returned before the child exited")
		}

		// Clients already know this URL. A restart collision must fail without
		// changing it, even though the log contains an earlier startup message.
		restartOccupant, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", h.serverPort))
		if err != nil {
			t.Fatalf("occupy restart address: %v", err)
		}
		defer func() { _ = restartOccupant.Close() }()
		if err := h.StartServer(); !errors.Is(err, errServerAddressInUse) {
			t.Fatalf("expected restart collision, got: %v", err)
		}
		if h.ServerURL != url || h.serverCmd != nil {
			t.Fatal("failed restart changed the URL or retained the child")
		}
		if err := restartOccupant.Close(); err != nil {
			t.Fatal(err)
		}
		if err := h.StartServer(); err != nil {
			t.Fatalf("restart after releasing address: %v", err)
		}
		if h.ServerURL != url {
			t.Fatal("restart changed the server URL")
		}
	})

	t.Run("collision retries are bounded", func(t *testing.T) {
		port, occupant := occupySelectedPort(t)
		defer func() { _ = occupant.Close() }()
		h := startupHarness(t, bin, port)
		attempts := 0
		err := h.startServerWithRetry(func() (int, error) {
			attempts++
			if h.serverCmd != nil {
				t.Fatal("previous child was not cleaned up before retry")
			}
			return port, nil
		})
		if !errors.Is(err, errServerAddressInUse) || attempts != serverStartAttempts {
			t.Fatalf("expected %d collision attempts, got %d: %v", serverStartAttempts, attempts, err)
		}
		if h.serverCmd != nil {
			t.Fatal("retry exhaustion retained the child")
		}
	})

	t.Run("unrelated failure is not retried using stale logs", func(t *testing.T) {
		h := startupHarness(t, bin, 0)
		port, err := randomPort()
		if err != nil {
			t.Fatal(err)
		}
		staleLog := fmt.Sprintf("msg=\"server started\"\nlisten: listen tcp 127.0.0.1:%d: bind: address already in use\n", port)
		if err := os.WriteFile(h.serverLog, []byte(staleLog), 0600); err != nil {
			t.Fatal(err)
		}
		h.SetServerEnv("SYNC_SERVER_DB_PATH=" + h.serverData) // A directory cannot be opened as SQLite.
		attempts := 0
		err = h.startServerWithRetry(func() (int, error) {
			attempts++
			return port, nil
		})
		if err == nil || !strings.Contains(err.Error(), "open server db") || errors.Is(err, errServerAddressInUse) {
			t.Fatalf("expected database failure, got: %v", err)
		}
		if attempts != 1 || h.serverCmd != nil {
			t.Fatalf("unrelated failure retried or retained child: attempts=%d", attempts)
		}
	})

	t.Run("health timeout kills and reaps child", func(t *testing.T) {
		unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer unhealthy.Close()
		h := startupHarness(t, bin, 0)
		// First reach a healthy child so this test does not depend on startup
		// speed. Restart it with health requests directed to a failing endpoint.
		if err := h.startServerWithRetry(randomPort); err != nil {
			t.Fatal(err)
		}
		if err := h.StopServer(); err != nil {
			t.Fatal(err)
		}
		h.ServerURL = unhealthy.URL
		err := h.startServer(2 * time.Second)
		if err == nil || !strings.Contains(err.Error(), "health check timed out") || errors.Is(err, errServerAddressInUse) {
			t.Fatalf("expected health timeout, got: %v", err)
		}
		if !strings.Contains(err.Error(), `msg="server started"`) {
			t.Fatalf("child did not reach startup before health timeout: %v", err)
		}
		if h.serverCmd != nil {
			t.Fatal("health timeout retained the child")
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", h.serverPort))
		if err != nil {
			t.Fatalf("timed-out child still owns the address: %v", err)
		}
		_ = listener.Close()
	})

	t.Run("launch failure is not retried", func(t *testing.T) {
		h := startupHarness(t, filepath.Join(t.TempDir(), "missing-td-sync"), 0)
		attempts := 0
		err := h.startServerWithRetry(func() (int, error) {
			attempts++
			return randomPort()
		})
		if err == nil || attempts != 1 || h.serverCmd != nil {
			t.Fatalf("expected one failed launch with no child: attempts=%d err=%v", attempts, err)
		}
	})
}

func occupySelectedPort(t *testing.T) (int, net.Listener) {
	t.Helper()
	port, err := randomPort()
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the race: another socket takes the selected address between
	// randomPort releasing it and the child starting.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("occupy selected port: %v", err)
	}
	return port, listener
}

func startupHarness(t *testing.T, bin string, port int) *Harness {
	t.Helper()
	dir := t.TempDir()
	h := &Harness{
		SyncBin:    bin,
		WorkDir:    dir,
		ServerURL:  fmt.Sprintf("http://127.0.0.1:%d", port),
		serverPort: port,
		serverData: dir,
		serverLog:  filepath.Join(dir, "server.log"),
	}
	t.Cleanup(h.Teardown)
	return h
}
