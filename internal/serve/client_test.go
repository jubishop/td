package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marcus/td/internal/models"
)

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

func TestClientStaleWritesPreserveAgentChange(t *testing.T) {
	srv := newTestServerWithDB(t)
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

func TestClientRecordedApprovalRevisionGuard(t *testing.T) {
	t.Setenv("TD_FEATURE_REVIEW_POLICY_MODE", "trusted")
	srv := newTestServerWithDB(t)
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

func TestClientMoveUnpositionedTasks(t *testing.T) {
	srv := newTestServerWithDB(t)
	board, err := srv.db.CreateBoard("Client ordering", "")
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

func TestClientParentCycleRejected(t *testing.T) {
	srv := newTestServerWithDB(t)
	parent := &models.Issue{Title: "Parent task for client test"}
	if err := srv.db.CreateIssue(parent); err != nil {
		t.Fatal(err)
	}
	child := &models.Issue{Title: "Child task for client test", ParentID: parent.ID}
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

func TestClientProjectMetadata(t *testing.T) {
	srv := newTestServerWithDB(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, env := doJSON(t, ts, "GET", "/v1/project", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metadata: %+v", env)
	}
	data := env.Data.(map[string]interface{})
	if data["path"] != srv.baseDir || data["session_id"] != srv.sessionID {
		t.Fatalf("identity: %+v", data)
	}
	if data["title_min_length"].(float64) <= 0 || data["title_max_length"].(float64) < data["title_min_length"].(float64) {
		t.Fatalf("title limits: %+v", data)
	}
	caps := data["capabilities"].([]interface{})
	if len(caps) != 2 || caps[0] != "issue_revisions" || caps[1] != "board_move" {
		t.Fatalf("capabilities: %+v", caps)
	}
	for _, path := range []string{"/", "/assets/app.js", "/v1/browser", "/v1/markdown"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("API server exposes UI route %s: %d", path, w.Code)
		}
	}
}
