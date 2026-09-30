package db

import (
	"errors"
	"testing"

	"github.com/marcus/td/internal/models"
)

func TestConditionalIssueWritesCheckInsideLock(t *testing.T) {
	database, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	issue := &models.Issue{Title: "Task before concurrent write"}
	if err := database.CreateIssue(issue); err != nil {
		t.Fatal(err)
	}
	stale, _ := database.GetIssue(issue.ID)
	draft := *stale
	draft.Title = "Browser draft"
	agent, _ := database.GetIssue(issue.ID)
	agent.Title = "Agent change"
	if err := database.UpdateIssueLogged(agent, "ses_agent", models.ActionUpdate); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateIssueLoggedIfUnchanged(&draft, stale, "ses_web", models.ActionUpdate); !errors.Is(err, ErrIssueChanged) {
		t.Fatalf("stale update: %v", err)
	}
	if err := database.DeleteIssueLoggedIfUnchanged(issue.ID, "ses_web", stale); !errors.Is(err, ErrIssueChanged) {
		t.Fatalf("stale deletion: %v", err)
	}
	current, _ := database.GetIssue(issue.ID)
	if current.Title != "Agent change" {
		t.Fatalf("agent change overwritten: %s", current.Title)
	}
	draft = *current
	draft.Description = "Reconciled draft"
	if err := database.UpdateIssueLoggedIfUnchanged(&draft, current, "ses_web", models.ActionUpdate); err != nil {
		t.Fatal(err)
	}
}

func TestConditionalReviewWritesPreserveAgentChange(t *testing.T) {
	database, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	issue := &models.Issue{Title: "Task awaiting browser approval", Status: models.StatusInReview}
	if err := database.CreateIssue(issue); err != nil {
		t.Fatal(err)
	}
	stale, _ := database.GetIssue(issue.ID)
	agent := *stale
	agent.Title = "Agent revised acceptance evidence"
	if err := database.UpdateIssueLogged(&agent, "ses_agent", models.ActionUpdate); err != nil {
		t.Fatal(err)
	}
	draft := *stale
	draft.Status = models.StatusClosed
	review := NewReview{IssueID: issue.ID, ReviewerSession: "ses_web", Decision: "approved", Summary: "Verified evidence"}
	if _, err := database.CreateIssueReviewAndUpdateIssueLoggedIfUnchanged(review, &draft, stale, models.StatusInReview, "ses_web", models.ActionApprove); !errors.Is(err, ErrIssueChanged) {
		t.Fatalf("stale approval: %v", err)
	}
	if err := database.UpdateIssueLoggedWithReviewMetaIfUnchanged(&draft, stale, models.StatusInReview, "ses_web", models.ActionCloseAfterReview, "", ""); !errors.Is(err, ErrIssueChanged) {
		t.Fatalf("stale close after review: %v", err)
	}
	reviews, err := database.ListIssueReviews(issue.ID)
	if err != nil || len(reviews) != 0 {
		t.Fatalf("stale approval recorded reviews: %v, %v", reviews, err)
	}
	current, _ := database.GetIssue(issue.ID)
	if current.Status != models.StatusInReview || current.Title != agent.Title {
		t.Fatalf("agent change lost: %+v", current)
	}
	draft = *current
	draft.Status = models.StatusClosed
	if _, err := database.CreateIssueReviewAndUpdateIssueLoggedIfUnchanged(review, &draft, current, models.StatusInReview, "ses_web", models.ActionApprove); err != nil {
		t.Fatal(err)
	}
	active, err := database.GetActiveApprovalReview(issue.ID)
	if err != nil || active == nil || active.Summary != review.Summary {
		t.Fatalf("fresh approval missing: %+v, %v", active, err)
	}
}

func TestConditionalUpdateRollsBackReviewInvalidation(t *testing.T) {
	database, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	issue := &models.Issue{Title: "Task with recorded approval", Status: models.StatusInReview}
	if err := database.CreateIssue(issue); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateIssueReview(NewReview{IssueID: issue.ID, ReviewerSession: "ses_review", Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.conn.Exec(`CREATE TRIGGER fail_browser_update BEFORE INSERT ON action_log
		WHEN NEW.entity_type = 'issue' AND NEW.action_type = 'update'
		BEGIN SELECT RAISE(FAIL, 'injected issue log failure'); END`); err != nil {
		t.Fatal(err)
	}
	original, _ := database.GetIssue(issue.ID)
	draft := *original
	draft.Title = "Browser changes reviewed content"
	if err := database.UpdateIssueLoggedIfUnchanged(&draft, original, "ses_web", models.ActionUpdate); err == nil {
		t.Fatal("expected injected failure")
	}
	current, _ := database.GetIssue(issue.ID)
	active, err := database.GetActiveApprovalReview(issue.ID)
	if current.Title != original.Title || err != nil || active == nil {
		t.Fatalf("failed save partially changed issue or review: %+v, %+v, %v", current, active, err)
	}
}
