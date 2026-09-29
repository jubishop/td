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
	defer database.Close()
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
