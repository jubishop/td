package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/td/internal/serverdb"
)

// The tests run admin commands in a child copy of this test binary, so each
// one is a separate process, as on a real server, and os.Exit paths can be
// checked.
const runAdminEnv = "TD_SYNC_TEST_RUN_ADMIN"

func TestMain(m *testing.M) {
	if os.Getenv(runAdminEnv) == "1" {
		runAdmin(os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runAdminProcess runs an admin command with stdin at EOF, as when it runs
// non-interactively.
func runAdminProcess(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runAdminProcessInput(t, "", args...)
}

func runAdminProcessInput(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), runAdminEnv+"=1")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run admin %v: %v", args, err)
	}
	return out.String(), errOut.String(), code
}

// openServerStore opens the database the way a running td-sync server does
// and keeps it open for the whole test.
func openServerStore(t *testing.T) (*serverdb.ServerDB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.db")
	store, err := serverdb.Open(path)
	if err != nil {
		t.Fatalf("open server db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func startDeviceLogin(t *testing.T, store *serverdb.ServerDB, u *serverdb.User, n int) string {
	t.Helper()
	dch := fmt.Sprintf("%064d", n)
	cc, method := "challenge", "S256"
	selector, _, err := store.CreateEmailChallenge(u.Email, "device_login", u.ID, serverdb.ChallengeOptions{
		DeviceCodeHash:      &dch,
		CodeChallenge:       &cc,
		CodeChallengeMethod: &method,
	})
	if err != nil {
		t.Fatalf("create device challenge: %v", err)
	}
	return selector
}

func challengeStatus(t *testing.T, store *serverdb.ServerDB, selector string) string {
	t.Helper()
	c, err := store.LookupChallenge(selector)
	if err != nil || c == nil {
		t.Fatalf("lookup challenge %s: %v (found=%v)", selector, err, c != nil)
	}
	return c.Status
}

func TestAdminApproveLogin_Errors(t *testing.T) {
	store, dbPath := openServerStore(t)

	_, stderr, code := runAdminProcess(t, "approve-login", "--db", dbPath)
	if code != 1 || !strings.Contains(stderr, "--email is required") {
		t.Errorf("missing --email: code=%d stderr=%q", code, stderr)
	}

	_, stderr, code = runAdminProcess(t, "approve-login", "--email", "nobody@example.com", "--db", dbPath)
	if code != 1 || !strings.Contains(stderr, "user not found") ||
		!strings.Contains(stderr, "td-sync admin create-user --email nobody@example.com") {
		t.Errorf("unknown user: code=%d stderr=%q", code, stderr)
	}

	if _, err := store.CreateUser("idle@example.com"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, stderr, code = runAdminProcess(t, "approve-login", "--email", "idle@example.com", "--db", dbPath)
	if code != 1 || !strings.Contains(stderr, "no pending login for idle@example.com") {
		t.Errorf("no pending login: code=%d stderr=%q", code, stderr)
	}
}

func TestAdminApproveLogin_MultipleNeedSelector(t *testing.T) {
	store, dbPath := openServerStore(t)
	u, err := store.CreateUser("two@example.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	first := startDeviceLogin(t, store, u, 1)
	second := startDeviceLogin(t, store, u, 2)

	// Without a selector, several pending logins are listed and none is
	// approved, even with --yes or a "y" on stdin.
	for _, args := range [][]string{nil, {"--yes"}} {
		args = append([]string{"approve-login", "--email", "two@example.com", "--db", dbPath}, args...)
		_, stderr, code := runAdminProcessInput(t, "y\n", args...)
		if code != 1 || !strings.Contains(stderr, "2 pending logins") ||
			!strings.Contains(stderr, first) || !strings.Contains(stderr, second) {
			t.Fatalf("ambiguous %v: code=%d stderr=%q", args, code, stderr)
		}
		for _, sel := range []string{first, second} {
			if got := challengeStatus(t, store, sel); got != serverdb.ChallengeStatusPending {
				t.Fatalf("ambiguous %v changed %s to %s", args, sel, got)
			}
		}
	}

	stdout, stderr, code := runAdminProcess(t, "approve-login", "--email", "TWO@example.com", "--selector", second, "--db", dbPath)
	if code != 0 || !strings.Contains(stdout, "approved login for two@example.com") || !strings.Contains(stdout, "selector:   "+second) ||
		strings.Contains(stdout, "Approve this login?") {
		t.Fatalf("approve with selector: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := challengeStatus(t, store, second); got != serverdb.ChallengeStatusVerified {
		t.Errorf("selected login: got %s, want verified", got)
	}
	if got := challengeStatus(t, store, first); got != serverdb.ChallengeStatusPending {
		t.Errorf("other login: got %s, want pending", got)
	}

	_, stderr, code = runAdminProcess(t, "approve-login", "--email", "two@example.com", "--selector", second, "--db", dbPath)
	if code != 1 || !strings.Contains(stderr, "no pending login "+second) {
		t.Errorf("re-approve: code=%d stderr=%q", code, stderr)
	}
}

// TestAdminApproveLogin_SinglePendingNeedsConfirmation covers the case where
// exactly one login is pending and no selector is given: the command shows it
// and approves it only after an explicit yes.
func TestAdminApproveLogin_SinglePendingNeedsConfirmation(t *testing.T) {
	const prompt = "Approve this login? [y/N] "
	cases := []struct {
		name     string
		stdin    string
		flags    []string // "SELECTOR" is replaced with the pending selector
		approved bool
		prompted bool
	}{
		{name: "y", stdin: "y\n", approved: true, prompted: true},
		{name: "Yes", stdin: "Yes\n", approved: true, prompted: true},
		{name: "n", stdin: "n\n", prompted: true},
		{name: "empty line", stdin: "\n", prompted: true},
		{name: "EOF", stdin: "", prompted: true},
		{name: "--yes", flags: []string{"--yes"}, approved: true},
		{name: "--selector", flags: []string{"--selector", "SELECTOR"}, approved: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, dbPath := openServerStore(t)
			u, err := store.CreateUser("one@example.com")
			if err != nil {
				t.Fatalf("create user: %v", err)
			}
			selector := startDeviceLogin(t, store, u, 1)

			args := []string{"approve-login", "--email", "one@example.com", "--db", dbPath}
			for _, f := range tc.flags {
				if f == "SELECTOR" {
					f = selector
				}
				args = append(args, f)
			}
			stdout, stderr, code := runAdminProcessInput(t, tc.stdin, args...)

			if got := strings.Contains(stdout, prompt); got != tc.prompted {
				t.Errorf("prompted=%v, want %v; stdout=%q", got, tc.prompted, stdout)
			}
			if i := strings.Index(stdout, prompt); i >= 0 && !strings.Contains(stdout[:i], "selector:   "+selector) {
				t.Errorf("prompt did not show the pending login first; stdout=%q", stdout)
			}
			status := challengeStatus(t, store, selector)
			if tc.approved {
				if code != 0 || status != serverdb.ChallengeStatusVerified || !strings.Contains(stdout, "approved login for one@example.com") {
					t.Fatalf("want approved: code=%d status=%s stdout=%q stderr=%q", code, status, stdout, stderr)
				}
				return
			}
			if code != 1 || status != serverdb.ChallengeStatusPending || !strings.Contains(stderr, "login not approved") ||
				strings.Contains(stdout, "approved login for") {
				t.Fatalf("want not approved: code=%d status=%s stdout=%q stderr=%q", code, status, stdout, stderr)
			}
		})
	}
}

// TestAdminApproveLogin_WhileServerWrites runs approve-login as a separate
// process while this process holds the database open and keeps writing to it,
// as a running td-sync server would. A real server writes server.db once or
// twice per request; this writer commits hundreds of times a second but, like
// any real server, leaves the lock free between writes. A writer that never
// releases the lock can starve any second process past the busy timeout.
func TestAdminApproveLogin_WhileServerWrites(t *testing.T) {
	store, dbPath := openServerStore(t)

	// create-user runs before the load starts: its transaction reads before it
	// writes, so a concurrent writer can make it fail with SQLITE_BUSY. The
	// docs tell operators to run it while the server is stopped.
	stdout, stderr, code := runAdminProcess(t, "create-user", "--email", "live@example.com", "--db", dbPath)
	if code != 0 || !strings.Contains(stdout, "created user live@example.com") {
		t.Fatalf("create-user: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	u, err := store.GetUserByEmail("live@example.com")
	if err != nil || u == nil {
		t.Fatalf("server handle cannot see created user: %v", err)
	}
	selector := startDeviceLogin(t, store, u, 3)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writes atomic.Int64
	var writeErr atomic.Value
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := store.InsertAuthEvent("load", "load@example.com", serverdb.AuthEventStarted, "{}"); err != nil {
				writeErr.Store(err.Error())
				return
			}
			writes.Add(1)
			time.Sleep(time.Millisecond)
		}
	}()
	var once sync.Once
	stopWriter := func() {
		once.Do(func() { close(stop) })
		wg.Wait()
	}
	t.Cleanup(stopWriter)

	before, began := writes.Load(), time.Now()
	stdout, stderr, code = runAdminProcess(t, "approve-login", "--email", "live@example.com", "--yes", "--db", dbPath)
	t.Logf("server committed %d writes during the %v approve-login process", writes.Load()-before, time.Since(began).Round(time.Millisecond))
	if code != 0 || !strings.Contains(stdout, "selector:   "+selector) {
		t.Fatalf("approve-login while server writes: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if writes.Load() == before {
		t.Fatal("server writer made no progress while approve-login ran")
	}

	stopWriter()
	if v := writeErr.Load(); v != nil {
		t.Fatalf("server write failed while approve-login ran: %v", v)
	}
	if got := challengeStatus(t, store, selector); got != serverdb.ChallengeStatusVerified {
		t.Fatalf("server handle sees status %s, want verified", got)
	}
}
