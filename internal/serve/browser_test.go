package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcus/td/internal/models"
)

func newBrowserTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServerWithDB(t)
	srv.config.Browser = true
	srv.registerBrowserRoutes()
	return srv
}

func TestBrowserEmbeddedAssets(t *testing.T) {
	srv := newBrowserTestServer(t)
	for _, path := range []string{"/", "/assets/style.css", "/assets/app.js", "/assets/api.js", "/assets/details.js", "/assets/workspace.js", "/assets/ui.js"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:8090"+path, nil)
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("%s: status %d, %s", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("missing CSP for %s", path)
		}
	}
	response := httptest.NewRecorder()
	newTestServer(ServeConfig{}).Handler().ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("API-only server serves browser: %d", response.Code)
	}
}

func TestBrowserOriginBoundary(t *testing.T) {
	srv := newBrowserTestServer(t)
	for _, tc := range []struct {
		host, origin, site string
		want               int
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080", "same-origin", 200},
		{"localhost:8080", "", "none", 200},
		{"[::1]:8080", "http://[::1]:8080", "same-origin", 200},
		{"127.0.0.1:8080", "https://example.com", "cross-site", 403},
		{"127.0.0.1:8080", "http://127.0.0.1:9090", "same-site", 403},
		{"127.0.0.1:8080", "null", "", 403},
		{"attacker.example:8080", "", "", 403},
		{"127.0.0.1:8080", "", "cross-site", 403},
	} {
		t.Run(tc.host+tc.origin+tc.site, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+"/v1/browser", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBrowserMarkdownSanitization(t *testing.T) {
	srv := newBrowserTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	_, env := doJSON(t, ts, "POST", "/v1/markdown", map[string]string{"text": "# Heading\n\n- [x] Done\n\n[bad](javascript:alert(1))\n\n<script>alert(1)</script><img src=x onerror=alert(2)>\n\n```go\nvar ok = true\n```"})
	html := env.Data.(map[string]interface{})["html"].(string)
	for _, forbidden := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("unsafe HTML: %s", html)
		}
	}
	for _, expected := range []string{"<h1>Heading</h1>", "<pre>", "<code", `<input`, `disabled`, `checked`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("missing Markdown output %s: %s", expected, html)
		}
	}
}

func requestRevision(t *testing.T, ts *httptest.Server, method, path, revision string, body interface{}) int {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(data))
	req.Header.Set("If-Match", `"`+revision+`"`)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestBrowserStaleWritesPreserveAgentChange(t *testing.T) {
	srv := newBrowserTestServer(t)
	issue := &models.Issue{Title: "Original task", Status: models.StatusInReview}
	if err := srv.db.CreateIssue(issue); err != nil {
		t.Fatal(err)
	}
	loaded, _ := srv.db.GetIssue(issue.ID)
	oldRevision := issueRevision(loaded)
	loaded.Title = "Agent updated the task"
	if err := srv.db.UpdateIssueLogged(loaded, "ses_agent", models.ActionUpdate); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, tc := range []struct {
		method, path string
		body         interface{}
	}{
		{"PATCH", "/v1/issues/" + issue.ID, map[string]string{"title": "Browser drafted a different title"}},
		{"POST", "/v1/issues/" + issue.ID + "/approve", map[string]string{"reason": "Reviewed stale content"}},
		{"DELETE", "/v1/issues/" + issue.ID, nil},
	} {
		if status := requestRevision(t, ts, tc.method, tc.path, oldRevision, tc.body); status != http.StatusConflict {
			t.Fatalf("%s: got %d", tc.method, status)
		}
	}
	current, _ := srv.db.GetIssue(issue.ID)
	if current.Title != "Agent updated the task" || current.Status != models.StatusInReview || current.DeletedAt != nil {
		t.Fatalf("stale write changed task: %+v", current)
	}
	if status := requestRevision(t, ts, "PATCH", "/v1/issues/"+issue.ID, issueRevision(current), map[string]string{"description": "Explicitly reconciled"}); status != http.StatusOK {
		t.Fatalf("fresh save: %d", status)
	}
	current, _ = srv.db.GetIssue(issue.ID)
	if current.Title != "Agent updated the task" || current.Description != "Explicitly reconciled" {
		t.Fatalf("partial save lost fields: %+v", current)
	}
}

func TestBrowserRecordedApprovalRevisionGuard(t *testing.T) {
	t.Setenv("TD_FEATURE_REVIEW_POLICY_MODE", "trusted")
	srv := newBrowserTestServer(t)
	id := seedInReviewIssue(t, srv.db, "ses_agent")
	original, _ := srv.db.GetIssue(id)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, env := doJSON(t, ts, "POST", "/v1/issues/"+id+"/reviews", map[string]string{
		"decision": "approved", "summary": "Verified current evidence",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record approval: %d %+v", resp.StatusCode, env.Error)
	}
	body := map[string]string{"reason": "Close using recorded approval"}
	if status := requestRevision(t, ts, "POST", "/v1/issues/"+id+"/approve", issueRevision(original), body); status != http.StatusConflict {
		t.Fatalf("stale close after review: %d", status)
	}
	current, _ := srv.db.GetIssue(id)
	if current.Status != models.StatusInReview {
		t.Fatalf("stale action closed task: %+v", current)
	}
	if status := requestRevision(t, ts, "POST", "/v1/issues/"+id+"/approve", issueRevision(current), body); status != http.StatusOK {
		t.Fatalf("fresh close after review: %d", status)
	}
	closed, _ := srv.db.GetIssue(id)
	reviews, err := srv.db.ListIssueReviews(id)
	if closed.Status != models.StatusClosed || closed.ReviewerSession != current.ReviewerSession || err != nil || len(reviews) != 1 {
		t.Fatalf("recorded approval not preserved: %+v, %v, %v", closed, reviews, err)
	}
}

func TestBrowserMoveUnpositionedTasks(t *testing.T) {
	srv := newBrowserTestServer(t)
	board, err := srv.db.CreateBoard("Browser ordering", "")
	if err != nil {
		t.Fatal(err)
	}
	var issues []models.Issue
	for _, title := range []string{"First task", "Second task", "Third task"} {
		issue := models.Issue{Title: title}
		if err := srv.db.CreateIssue(&issue); err != nil {
			t.Fatal(err)
		}
		issues = append(issues, issue)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, move := range []struct {
		id, before string
		want       []string
	}{
		{issues[2].ID, issues[0].ID, []string{issues[2].ID, issues[0].ID, issues[1].ID}},
		{issues[2].ID, "", []string{issues[0].ID, issues[1].ID, issues[2].ID}},
		{issues[2].ID, issues[1].ID, []string{issues[0].ID, issues[2].ID, issues[1].ID}},
	} {
		resp, env := doJSON(t, ts, "POST", "/v1/boards/"+board.ID+"/move", map[string]string{"issue_id": move.id, "before_id": move.before})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("move: %+v", env.Error)
		}
		views, err := srv.db.GetBoardIssues(board.ID, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range move.want {
			if views[i].Issue.ID != want {
				t.Fatalf("position %d = %s, want %s", i, views[i].Issue.ID, want)
			}
		}
	}
}

func TestBrowserParentCycleRejected(t *testing.T) {
	srv := newBrowserTestServer(t)
	parent := &models.Issue{Title: "Parent task for browser test"}
	if err := srv.db.CreateIssue(parent); err != nil {
		t.Fatal(err)
	}
	child := &models.Issue{Title: "Child task for browser test", ParentID: parent.ID}
	if err := srv.db.CreateIssue(child); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, parentID := range []string{parent.ID, child.ID} {
		resp, _ := doJSON(t, ts, "PATCH", "/v1/issues/"+parent.ID, map[string]string{"parent_id": parentID})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("cycle accepted: %d", resp.StatusCode)
		}
	}
}
