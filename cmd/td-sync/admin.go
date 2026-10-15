package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marcus/td/internal/api"
	"github.com/marcus/td/internal/serverdb"
)

func runAdmin(args []string) {
	if len(args) == 0 {
		printAdminUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "create-user":
		runAdminCreateUser(args[1:])
	case "approve-login":
		runAdminApproveLogin(args[1:])
	case "grant":
		runAdminGrant(args[1:])
	case "revoke":
		runAdminRevoke(args[1:])
	case "create-key":
		runAdminCreateKey(args[1:])
	case "revoke-key":
		runAdminRevokeKey(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown admin command: %s\n", args[0])
		printAdminUsage()
		os.Exit(1)
	}
}

func printAdminUsage() {
	fmt.Fprintln(os.Stderr, `Usage: td-sync admin <command> [flags]

Commands:
  create-user   Create a user (idempotent)
  approve-login Approve a pending td auth login without email
  grant         Grant admin privileges to a user
  revoke        Revoke admin privileges from a user
  create-key    Create an API key for an admin user
  revoke-key    Revoke an API key by key ID`)
}

func openDB(dbPath string) *serverdb.ServerDB {
	if dbPath == "" {
		cfg := api.LoadConfig()
		dbPath = cfg.ServerDBPath
	}
	store, err := serverdb.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		os.Exit(1)
	}
	return store
}

// runAdminCreateUser provisions a user by email, for self-hosted servers and
// dev/test setup (e.g. the e2e harness), where the non-enumerating device-login
// flow requires the user to already exist. It is idempotent: if the user already
// exists it prints the existing ID and exits 0. Safe to run while the server
// is up.
func runAdminCreateUser(args []string) {
	fs := flag.NewFlagSet("admin create-user", flag.ExitOnError)
	email := fs.String("email", "", "user email address")
	dbPath := fs.String("db", "", "path to server.db (default: from SYNC_SERVER_DB_PATH or ./data/server.db)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		fs.Usage()
		os.Exit(1)
	}

	store := openDB(*dbPath)
	defer func() { _ = store.Close() }()

	existing, err := store.GetUserByEmail(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if existing != nil {
		fmt.Printf("user already exists: %s (%s)\n", existing.Email, existing.ID)
		return
	}

	user, err := store.CreateUser(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("created user %s (%s)\n", user.Email, user.ID)
}

// runAdminApproveLogin approves a pending `td auth login` for an existing user
// without the emailed link, for self-hosted servers that send no email. The
// operator must choose the login explicitly: by --selector, or by confirming
// the only pending one after seeing its details. Nothing is approved
// implicitly, because per-email rate limiting can silently drop the operator's
// own request and leave someone else's as the only one pending.
// The waiting CLI still has to present its PKCE verifier on its next poll
// before a key is issued. Safe to run while the server is up.
func runAdminApproveLogin(args []string) {
	fs := flag.NewFlagSet("admin approve-login", flag.ExitOnError)
	email := fs.String("email", "", "email of the user whose pending login to approve")
	selector := fs.String("selector", "", "approve this pending login without prompting")
	yes := fs.Bool("yes", false, "approve the only pending login without prompting (for scripts)")
	dbPath := fs.String("db", "", "path to server.db (default: from SYNC_SERVER_DB_PATH or ./data/server.db)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		fs.Usage()
		os.Exit(1)
	}

	store := openDB(*dbPath)
	defer func() { _ = store.Close() }()

	user, err := store.GetUserByEmail(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if user == nil {
		fmt.Fprintf(os.Stderr, "error: user not found: %s\n", *email)
		fmt.Fprintf(os.Stderr, "create the account first: td-sync admin create-user --email %s\n", *email)
		os.Exit(1)
	}

	chosen := *selector
	shown := false
	if chosen == "" {
		pending, err := store.PendingDeviceChallenges(user.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		switch len(pending) {
		case 0:
			exitNoPendingLogin("", user.Email)
		case 1:
		default:
			fmt.Fprintf(os.Stderr, "error: %d pending logins for %s; re-run with --selector to choose one:\n", len(pending), user.Email)
			for _, p := range pending {
				fmt.Fprintln(os.Stderr)
				printPendingLogin(os.Stderr, p)
			}
			os.Exit(1)
		}

		p := pending[0]
		if !*yes {
			fmt.Printf("pending login for %s:\n", user.Email)
			printPendingLogin(os.Stdout, p)
			shown = true
			fmt.Print("Approve this login? [y/N] ")
			reader := bufio.NewReader(os.Stdin)
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Println()
			}
			line = strings.TrimSpace(strings.ToLower(line))
			if line != "y" && line != "yes" {
				fmt.Fprintln(os.Stderr, "login not approved")
				os.Exit(1)
			}
		}
		chosen = p.Selector
	}

	c, err := store.ApproveDeviceChallengeAsOperator(user.ID, chosen)
	if errors.Is(err, serverdb.ErrChallengeNotFound) {
		exitNoPendingLogin(chosen, user.Email)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("approved login for %s\n", user.Email)
	if !shown {
		printPendingLogin(os.Stdout, c)
	}
	fmt.Println("The waiting `td auth login` completes on its next poll.")
}

func exitNoPendingLogin(selector, email string) {
	if selector != "" {
		fmt.Fprintf(os.Stderr, "error: no pending login %s for %s\n", selector, email)
	} else {
		fmt.Fprintf(os.Stderr, "error: no pending login for %s\n", email)
	}
	fmt.Fprintln(os.Stderr, "run `td auth login` on the client first; pending logins expire after 15 minutes")
	os.Exit(1)
}

// printPendingLogin prints what the server recorded about a device login, so
// the operator can tell which client started it.
func printPendingLogin(w io.Writer, c *serverdb.EmailChallenge) {
	value := func(s *string) string {
		if s == nil || *s == "" {
			return "-"
		}
		return *s
	}
	_, _ = fmt.Fprintf(w, "  selector:   %s\n", c.Selector)
	_, _ = fmt.Fprintf(w, "  created:    %s\n", c.CreatedAt.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "  expires:    %s\n", c.ExpiresAt.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "  ip:         %s\n", value(c.IP))
	_, _ = fmt.Fprintf(w, "  user agent: %s\n", value(c.UserAgent))
}

func runAdminGrant(args []string) {
	fs := flag.NewFlagSet("admin grant", flag.ExitOnError)
	email := fs.String("email", "", "user email address")
	dbPath := fs.String("db", "", "path to server.db (default: from SYNC_SERVER_DB_PATH or ./data/server.db)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		fs.Usage()
		os.Exit(1)
	}

	store := openDB(*dbPath)
	defer func() { _ = store.Close() }()

	if err := store.SetUserAdmin(*email, true); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("granted admin to %s\n", strings.ToLower(strings.TrimSpace(*email)))
}

func runAdminRevoke(args []string) {
	fs := flag.NewFlagSet("admin revoke", flag.ExitOnError)
	email := fs.String("email", "", "user email address")
	dbPath := fs.String("db", "", "path to server.db (default: from SYNC_SERVER_DB_PATH or ./data/server.db)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		fs.Usage()
		os.Exit(1)
	}

	store := openDB(*dbPath)
	defer func() { _ = store.Close() }()

	// Check if this would remove the last admin
	count, err := store.CountAdmins()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Verify user is currently an admin before checking count
	user, err := store.GetUserByEmail(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if user == nil {
		fmt.Fprintf(os.Stderr, "error: user not found: %s\n", *email)
		os.Exit(1)
	}
	if user.IsAdmin && count <= 1 {
		fmt.Fprintln(os.Stderr, "error: cannot revoke last admin")
		os.Exit(1)
	}

	if err := store.SetUserAdmin(*email, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("revoked admin from %s\n", strings.ToLower(strings.TrimSpace(*email)))
}

func runAdminCreateKey(args []string) {
	fs := flag.NewFlagSet("admin create-key", flag.ExitOnError)
	email := fs.String("email", "", "admin user email address")
	scopes := fs.String("scopes", "", "comma-separated scopes (e.g. admin:read:server,sync)")
	name := fs.String("name", "", "key name (e.g. td-watch)")
	dbPath := fs.String("db", "", "path to server.db (default: from SYNC_SERVER_DB_PATH or ./data/server.db)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		fs.Usage()
		os.Exit(1)
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	store := openDB(*dbPath)
	defer func() { _ = store.Close() }()

	// Look up user
	user, err := store.GetUserByEmail(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if user == nil {
		fmt.Fprintf(os.Stderr, "error: user not found: %s\n", *email)
		os.Exit(1)
	}
	if !user.IsAdmin {
		fmt.Fprintf(os.Stderr, "error: user %s is not an admin\n", *email)
		os.Exit(1)
	}

	// Validate scopes
	scopeStr := *scopes
	if scopeStr == "" {
		scopeStr = "sync"
	}
	if err := api.ValidateScopes(scopeStr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	plaintext, ak, err := store.GenerateAPIKey(user.ID, *name, scopeStr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("created API key for %s\n", user.Email)
	fmt.Printf("  name:   %s\n", ak.Name)
	fmt.Printf("  scopes: %s\n", ak.Scopes)
	fmt.Printf("  key:    %s\n", plaintext)
	fmt.Println("\nSave this key now -- it will not be shown again.")
}

func runAdminRevokeKey(args []string) {
	fs := flag.NewFlagSet("admin revoke-key", flag.ExitOnError)
	email := fs.String("email", "", "user email address")
	keyID := fs.String("key-id", "", "API key ID to revoke")
	dbPath := fs.String("db", "", "path to server.db (default: from SYNC_SERVER_DB_PATH or ./data/server.db)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: --email is required")
		fs.Usage()
		os.Exit(1)
	}
	if *keyID == "" {
		fmt.Fprintln(os.Stderr, "error: --key-id is required")
		fs.Usage()
		os.Exit(1)
	}

	store := openDB(*dbPath)
	defer func() { _ = store.Close() }()

	// Look up user to confirm the email exists before attempting revocation.
	user, err := store.GetUserByEmail(*email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if user == nil {
		fmt.Fprintf(os.Stderr, "error: user not found: %s\n", *email)
		os.Exit(1)
	}

	// No auth event is emitted on the CLI path by design: the admin CLI has no
	// caller UserID context. Use the HTTP DELETE /admin/keys/:id endpoint for
	// audited revocation.
	if err := store.AdminRevokeAPIKey(*keyID); err != nil {
		if err == serverdb.ErrNotFound {
			fmt.Fprintf(os.Stderr, "error: key not found: %s\n", *keyID)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("revoked key %s from %s\n", *keyID, user.Email)
}
