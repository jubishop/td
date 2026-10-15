package serverdb

import (
	"errors"
	"testing"
	"time"
)

// newDeviceLoginChallenge creates a pending device_login challenge for u the
// way /v1/auth/device/start does, and returns its selector.
func newDeviceLoginChallenge(t *testing.T, db *ServerDB, u *User) string {
	t.Helper()
	dch := hashSecret("device-code-" + u.ID + time.Now().String())
	cc, method := "challenge", "S256"
	ip, ua := "192.0.2.10", "td-test"
	selector, _, err := db.CreateEmailChallenge(u.Email, "device_login", u.ID, ChallengeOptions{
		DeviceCodeHash:      &dch,
		CodeChallenge:       &cc,
		CodeChallengeMethod: &method,
		IP:                  &ip,
		UserAgent:           &ua,
	})
	if err != nil {
		t.Fatalf("create device challenge: %v", err)
	}
	return selector
}

func challengeStatus(t *testing.T, db *ServerDB, selector string) string {
	t.Helper()
	c, err := db.LookupChallenge(selector)
	if err != nil || c == nil {
		t.Fatalf("lookup challenge %s: %v (found=%v)", selector, err, c != nil)
	}
	return c.Status
}

func pendingSelectors(t *testing.T, db *ServerDB, userID string) []string {
	t.Helper()
	pending, err := db.PendingDeviceChallenges(userID)
	if err != nil {
		t.Fatalf("pending device challenges: %v", err)
	}
	var selectors []string
	for _, p := range pending {
		selectors = append(selectors, p.Selector)
	}
	return selectors
}

// ineligibleChallenges creates one challenge of each kind an operator must
// never approve for u, and returns their selectors.
func ineligibleChallenges(t *testing.T, db *ServerDB, u, other *User) map[string]string {
	t.Helper()
	expired := newDeviceLoginChallenge(t, db, u)
	db.ForceExpireChallengeForTest(expired, time.Now().UTC().Add(-time.Minute))

	consumed := newDeviceLoginChallenge(t, db, u)
	if _, err := db.conn.Exec(`UPDATE auth_email_challenges SET status = ? WHERE selector = ?`, ChallengeStatusConsumed, consumed); err != nil {
		t.Fatalf("mark consumed: %v", err)
	}

	web, _, err := db.CreateEmailChallenge(u.Email, "web_login", u.ID, ChallengeOptions{})
	if err != nil {
		t.Fatalf("create web challenge: %v", err)
	}

	return map[string]string{
		"expired":       expired,
		"consumed":      consumed,
		"web_login":     web,
		"other user's":  newDeviceLoginChallenge(t, db, other),
		"never existed": "0123456789abcdef0123456789abcdef",
	}
}

func TestPendingDeviceChallenges(t *testing.T) {
	db := newTestDB(t)
	u, err := db.CreateUser("pending@test.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	other, err := db.CreateUser("pending-other@test.com")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	if got := pendingSelectors(t, db, u.ID); len(got) != 0 {
		t.Fatalf("no challenges: got %v", got)
	}

	ineligibleChallenges(t, db, u, other)
	older := newDeviceLoginChallenge(t, db, u)
	newer := newDeviceLoginChallenge(t, db, u)
	if _, err := db.conn.Exec(`UPDATE auth_email_challenges SET created_at = ? WHERE selector = ?`,
		time.Now().UTC().Add(-time.Minute), older); err != nil {
		t.Fatalf("age challenge: %v", err)
	}

	got := pendingSelectors(t, db, u.ID)
	if len(got) != 2 || got[0] != newer || got[1] != older {
		t.Fatalf("pending: got %v, want [%s %s] (newest first, eligible only)", got, newer, older)
	}
	pending, err := db.PendingDeviceChallenges(u.ID)
	if err != nil {
		t.Fatalf("pending device challenges: %v", err)
	}
	if p := pending[0]; p.IP == nil || *p.IP != "192.0.2.10" || p.UserAgent == nil || *p.UserAgent != "td-test" {
		t.Errorf("listed challenge is missing ip/user agent: %+v", p)
	}

	// Listing changes nothing.
	for _, sel := range []string{older, newer} {
		if s := challengeStatus(t, db, sel); s != ChallengeStatusPending {
			t.Errorf("listing changed %s to %s", sel, s)
		}
	}
}

func TestApproveDeviceChallengeAsOperator_Succeeds(t *testing.T) {
	db := newTestDB(t)
	u, err := db.CreateUser("operator@test.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	selector := newDeviceLoginChallenge(t, db, u)
	sibling := newDeviceLoginChallenge(t, db, u)

	c, err := db.ApproveDeviceChallengeAsOperator(u.ID, selector)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if c.Selector != selector || c.Status != ChallengeStatusVerified || c.VerifiedAt == nil {
		t.Fatalf("approved challenge: got selector=%s status=%s verified_at=%v", c.Selector, c.Status, c.VerifiedAt)
	}
	if got := challengeStatus(t, db, selector); got != ChallengeStatusVerified {
		t.Fatalf("stored status: got %s, want verified", got)
	}
	if got := challengeStatus(t, db, sibling); got != ChallengeStatusPending {
		t.Fatalf("other pending login: got %s, want pending", got)
	}

	// The operator did not prove the address, so it stays unverified.
	found, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if found.EmailVerifiedAt != nil {
		t.Error("operator approval must not set email_verified_at")
	}

	events, err := db.QueryAuthEvents(AuthEventDeviceVerified, u.Email, "", "", 10, "")
	if err != nil {
		t.Fatalf("query auth events: %v", err)
	}
	if len(events.Data) != 1 {
		t.Fatalf("device_verified events: got %d, want 1", len(events.Data))
	}
	if ev := events.Data[0]; ev.AuthRequestID != selector || ev.Metadata != OperatorApprovalMetadata {
		t.Errorf("auth event: got request=%s metadata=%s", ev.AuthRequestID, ev.Metadata)
	}

	// An approved login cannot be approved twice.
	if _, err := db.ApproveDeviceChallengeAsOperator(u.ID, selector); !errors.Is(err, ErrChallengeNotFound) {
		t.Fatalf("second approve: got %v, want ErrChallengeNotFound", err)
	}
}

func TestApproveDeviceChallengeAsOperator_RefusesIneligible(t *testing.T) {
	db := newTestDB(t)
	u, err := db.CreateUser("ineligible@test.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	other, err := db.CreateUser("ineligible-other@test.com")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	for name, sel := range ineligibleChallenges(t, db, u, other) {
		if _, err := db.ApproveDeviceChallengeAsOperator(u.ID, sel); !errors.Is(err, ErrChallengeNotFound) {
			t.Errorf("%s challenge: got %v, want ErrChallengeNotFound", name, err)
		}
		if c, _ := db.LookupChallenge(sel); c != nil && c.Status == ChallengeStatusVerified {
			t.Errorf("%s challenge was marked verified", name)
		}
	}

	// A selector is required; there is no implicit choice.
	newDeviceLoginChallenge(t, db, u)
	if _, err := db.ApproveDeviceChallengeAsOperator(u.ID, ""); err == nil {
		t.Fatal("empty selector: got nil error")
	}
	if got := pendingSelectors(t, db, u.ID); len(got) != 1 {
		t.Fatalf("empty selector changed pending logins: %v", got)
	}

	events, err := db.QueryAuthEvents(AuthEventDeviceVerified, "", "", "", 10, "")
	if err != nil {
		t.Fatalf("query auth events: %v", err)
	}
	if len(events.Data) != 0 {
		t.Fatalf("refused approvals recorded %d device_verified events", len(events.Data))
	}
}
