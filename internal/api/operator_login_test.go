package api

import (
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/td/internal/email"
	"github.com/marcus/td/internal/serverdb"
	"github.com/marcus/td/internal/syncclient"
)

// TestCLIDeviceLogin_OperatorApproval drives `td auth login` against a server
// that sends no email (the default log provider) and completes it with the
// list-then-approve steps that `td-sync admin approve-login` performs. They
// run through a second store handle on the same database file, the way the
// admin CLI opens it, while the server keeps reading and writing through its
// own handle. PKCE is still enforced, and the issued key is a working sync key.
func TestCLIDeviceLogin_OperatorApproval(t *testing.T) {
	const userEmail = "operator-approved@example.com"

	srv, store := newTestServer(t)
	srv.emailSender = email.NewLogSender()
	srv.config.AllowSignup = false
	if _, err := store.CreateUser(userEmail); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	client := syncclient.New(ts.URL, "", "")
	pkce, err := syncclient.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	start, err := client.DeviceStart(userEmail, pkce.Challenge, pkce.Method, "td-cli@test")
	if err != nil {
		t.Fatalf("DeviceStart: %v", err)
	}
	if poll, err := client.DevicePoll(start.DeviceCode, pkce.Verifier); err != nil || poll.Status != "pending" {
		t.Fatalf("pre-approval poll: status=%v err=%v, want pending", poll, err)
	}

	// Keep the server busy on its own handle while the operator approves:
	// polls read through the HTTP server, and auth events write to its store.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var reads, writes atomic.Int64
	var loadErr atomic.Value
	other, err := syncclient.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := client.DevicePoll("unrelated-device-code", other.Verifier); err != nil {
				loadErr.Store("poll: " + err.Error())
				return
			}
			reads.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := store.InsertAuthEvent("load", "load@example.com", serverdb.AuthEventStarted, "{}"); err != nil {
				loadErr.Store("write: " + err.Error())
				return
			}
			writes.Add(1)
		}
	}()
	waitFor(t, func() bool { return reads.Load() > 5 && writes.Load() > 5 })

	operator, err := serverdb.Open(srv.config.ServerDBPath)
	if err != nil {
		t.Fatalf("open second store handle: %v", err)
	}
	t.Cleanup(func() { _ = operator.Close() })
	user, err := operator.GetUserByEmail(userEmail)
	if err != nil || user == nil {
		t.Fatalf("operator lookup user: %v (found=%v)", err, user != nil)
	}
	pending, err := operator.PendingDeviceChallenges(user.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("operator lists pending logins: got %d, err %v; want 1", len(pending), err)
	}
	if _, err := operator.ApproveDeviceChallengeAsOperator(user.ID, pending[0].Selector); err != nil {
		t.Fatalf("operator approve: %v", err)
	}

	r, w := reads.Load(), writes.Load()
	waitFor(t, func() bool { return reads.Load() > r+5 && writes.Load() > w+5 })
	close(stop)
	wg.Wait()
	if v := loadErr.Load(); v != nil {
		t.Fatalf("server load failed during operator approval: %v", v)
	}

	// A poller without the original verifier still gets nothing.
	if _, err := client.DevicePoll(start.DeviceCode, other.Verifier); err == nil || !strings.Contains(err.Error(), "invalid_verifier") {
		t.Fatalf("wrong-verifier poll after approval: got %v, want invalid_verifier", err)
	}

	complete, err := client.DevicePoll(start.DeviceCode, pkce.Verifier)
	if err != nil {
		t.Fatalf("post-approval poll: %v", err)
	}
	if complete.Status != "complete" || complete.APIKey == nil || *complete.APIKey == "" {
		t.Fatalf("post-approval poll: status=%q key=%v, want complete with key", complete.Status, complete.APIKey != nil)
	}

	authed := syncclient.New(ts.URL, *complete.APIKey, "test-device-id")
	project, err := authed.CreateProject("operator-approved", "")
	if err != nil {
		t.Fatalf("issued key failed to create a project: %v", err)
	}
	if _, err := authed.SyncStatus(project.ID); err != nil {
		t.Fatalf("issued key failed project sync status: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
