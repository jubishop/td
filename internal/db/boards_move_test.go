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

func newMoveBoard(t *testing.T, n int) (*DB, string, []models.Issue) {
	t.Helper()
	database, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	board, err := database.CreateBoard("Bounded move", "")
	if err != nil {
		t.Fatal(err)
	}
	var issues []models.Issue
	for i := 0; i < n; i++ {
		issue := models.Issue{Title: "Bounded move task " + string(rune('A'+i))}
		if err := database.CreateIssue(&issue); err != nil {
			t.Fatal(err)
		}
		issues = append(issues, issue)
	}
	return database, board.ID, issues
}

func assertBoardOrder(t *testing.T, database *DB, boardID string, candidates []models.Issue, want ...int) {
	t.Helper()
	views, err := database.ApplyBoardPositions(boardID, candidates)
	if err != nil {
		t.Fatal(err)
	}
	for i, idx := range want {
		if views[i].Issue.ID != candidates[idx].ID {
			t.Fatalf("slot %d = %s, want candidate %d (%s)", i, views[i].Issue.ID, idx, candidates[idx].ID)
		}
	}
}

func countPositions(t *testing.T, database *DB, boardID string) int {
	t.Helper()
	var n int
	if err := database.conn.QueryRow(`SELECT COUNT(*) FROM board_issue_positions WHERE board_id = ?`, boardID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBoardMovePositionsOnlyTasksAboveTheDrop(t *testing.T) {
	database, boardID, issues := newMoveBoard(t, 10)
	if err := database.MoveBoardIssueLogged(boardID, issues[7].ID, issues[3].ID, issues, "ses_test"); err != nil {
		t.Fatal(err)
	}
	if got := countPositions(t, database, boardID); got != 4 {
		t.Fatalf("positioned %d tasks, want 4 (three above the drop plus the moved task)", got)
	}
	assertBoardOrder(t, database, boardID, issues, 0, 1, 2, 7, 3, 4, 5, 6, 8, 9)

	// Moving to the top of an unpositioned board writes only the moved task.
	database, boardID, issues = newMoveBoard(t, 5)
	if err := database.MoveBoardIssueLogged(boardID, issues[4].ID, issues[0].ID, issues, "ses_test"); err != nil {
		t.Fatal(err)
	}
	if got := countPositions(t, database, boardID); got != 1 {
		t.Fatalf("positioned %d tasks, want 1", got)
	}
	assertBoardOrder(t, database, boardID, issues, 4, 0, 1, 2, 3)
}

func TestBoardMoveToEndAndRespace(t *testing.T) {
	database, boardID, issues := newMoveBoard(t, 4)
	if err := database.MoveBoardIssueLogged(boardID, issues[0].ID, "", issues, "ses_test"); err != nil {
		t.Fatal(err)
	}
	assertBoardOrder(t, database, boardID, issues, 1, 2, 3, 0)

	// Adjacent positions leave no gap, so the move must respace in order.
	for i, id := range []string{issues[1].ID, issues[2].ID} {
		if err := database.SetIssuePositionLogged(boardID, id, 10+i, "ses_test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.MoveBoardIssueLogged(boardID, issues[3].ID, issues[2].ID, issues, "ses_test"); err != nil {
		t.Fatal(err)
	}
	assertBoardOrder(t, database, boardID, issues, 1, 3, 2, 0)
}
