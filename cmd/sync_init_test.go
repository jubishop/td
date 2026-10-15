package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/td/internal/syncconfig"
)

// TestSyncInitAccount runs `td sync init` against an already linked project
// and checks which account it reports. The env-only case used to panic on a
// nil auth.json.
func TestSyncInitAccount(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		saved   *syncconfig.AuthCredentials
		account string
	}{
		{
			name:    "TD_AUTH_KEY without auth.json",
			envKey:  "env-key",
			account: "unknown (using TD_AUTH_KEY)",
		},
		{
			name:    "saved login",
			saved:   &syncconfig.AuthCredentials{APIKey: "saved-key", Email: "saved@example.com"},
			account: "saved@example.com",
		},
		{
			name:    "TD_AUTH_KEY overrides a different saved login",
			envKey:  "env-key",
			saved:   &syncconfig.AuthCredentials{APIKey: "saved-key", Email: "saved@example.com"},
			account: "unknown (using TD_AUTH_KEY)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndRestoreGlobals(t)
			t.Setenv("TD_AUTH_KEY", tt.envKey)
			t.Setenv("TD_SYNC_URL", "http://127.0.0.1:1")
			dir := setupSyncStateDir(t, "proj-linked", false) // also isolates HOME
			baseDirOverride = &dir
			if tt.saved != nil {
				if err := syncconfig.SaveAuth(tt.saved); err != nil {
					t.Fatalf("save auth: %v", err)
				}
			}

			// Keep the server URL, then keep the existing project link.
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe: %v", err)
			}
			_, _ = w.WriteString("\n\n")
			_ = w.Close()
			oldStdin := os.Stdin
			os.Stdin = r
			t.Cleanup(func() {
				os.Stdin = oldStdin
				_ = r.Close()
			})

			var runErr error
			out := captureStdout(t, func() {
				runErr = syncInitCmd.RunE(syncInitCmd, nil)
			})
			if runErr != nil {
				t.Fatalf("sync init: %v\n%s", runErr, out)
			}
			for _, want := range []string{
				"Authenticated as: " + tt.account,
				"Email:   " + tt.account,
				"Project: proj-linked",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}
