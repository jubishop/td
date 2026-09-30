package serve

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/marcus/td/internal/db"
	"github.com/marcus/td/internal/models"
	"github.com/marcus/td/internal/query"
)

// HandleMoveBoardIssue places an issue before another issue, or at the end.
func HandleMoveBoardIssue(ctx HandlerContext, w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueID  string `json:"issue_id"`
		BeforeID string `json:"before_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IssueID == "" {
		WriteError(w, ErrValidation, "issue_id is required", http.StatusBadRequest)
		return
	}
	board, err := ctx.DB.ResolveBoardRef(r.PathValue("id"))
	if err != nil {
		WriteError(w, ErrNotFound, "board not found", http.StatusNotFound)
		return
	}
	var issues []models.Issue
	if board.Query == "" {
		issues, err = ctx.DB.ListIssues(db.ListIssuesOptions{SortBy: "priority"})
	} else {
		issues, err = query.Execute(ctx.DB, board.Query, "", query.ExecuteOptions{})
	}
	if err != nil {
		WriteError(w, ErrInternal, "could not load board tasks", http.StatusInternalServerError)
		return
	}
	body.IssueID = db.NormalizeIssueID(body.IssueID)
	if body.BeforeID != "" {
		body.BeforeID = db.NormalizeIssueID(body.BeforeID)
	}
	if err := ctx.DB.MoveBoardIssueLogged(board.ID, body.IssueID, body.BeforeID, issues, ctx.SessionID); err != nil {
		if errors.Is(err, db.ErrBoardChanged) {
			WriteError(w, ErrConflict, "The task or its destination no longer matches this board. Any completed status change has been saved; refresh the board to see it.", http.StatusConflict)
			return
		}
		WriteError(w, ErrInternal, "could not reorder board", http.StatusInternalServerError)
		return
	}
	notifyChange(ctx)
	WriteSuccess(w, map[string]bool{"positioned": true}, http.StatusOK)
}

func (s *Server) handleMoveBoardIssue(w http.ResponseWriter, r *http.Request) {
	HandleMoveBoardIssue(s.handlerContext(), w, r)
}
