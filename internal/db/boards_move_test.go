package db

import (
	"testing"

	"github.com/marcus/td/internal/models"
)

func TestBoardMovePreservesReservedPositions(t *testing.T) {
	database, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	board, err := database.CreateBoard("Move test", "")
	if err != nil {
		t.Fatal(err)
	}
	var issues []models.Issue
	for _, title := range []string{"Hidden task", "Visible task one", "Visible task two"} {
		issue := models.Issue{Title: title}
		if err := database.CreateIssue(&issue); err != nil {
			t.Fatal(err)
		}
		issues = append(issues, issue)
	}
	if err := database.SetIssuePositionLogged(board.ID, issues[0].ID, PositionGap, "ses_test"); err != nil {
		t.Fatal(err)
	}
	if err := database.RemoveIssuePositionLogged(board.ID, issues[0].ID, "ses_test"); err != nil {
		t.Fatal(err)
	}
	if err := database.MoveBoardIssueLogged(board.ID, issues[1].ID, "", issues[1:2], "ses_test"); err != nil {
		t.Fatal(err)
	}
	if err := database.MoveBoardIssueLogged(board.ID, issues[2].ID, issues[1].ID, issues[1:], "ses_test"); err != nil {
		t.Fatal(err)
	}
	views, err := database.ApplyBoardPositions(board.ID, issues[1:])
	if err != nil {
		t.Fatal(err)
	}
	if views[0].Issue.ID != issues[2].ID || views[1].Issue.ID != issues[1].ID {
		t.Fatalf("wrong order: %+v", views)
	}
	var hiddenPosition int
	if err := database.conn.QueryRow(`SELECT position FROM board_issue_positions WHERE board_id = ? AND issue_id = ?`, board.ID, issues[0].ID).Scan(&hiddenPosition); err != nil {
		t.Fatal(err)
	}
	if hiddenPosition != PositionGap {
		t.Fatalf("changed hidden task position: %d", hiddenPosition)
	}
}
