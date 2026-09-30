package serve

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/marcus/td/internal/db"
)

// HandleMoveBoardIssue places an issue before another issue, or at the end.
// Pass include_closed when the client's board view shows closed tasks, so the
// move sees the same list the client displayed.
func HandleMoveBoardIssue(ctx HandlerContext, w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueID       string `json:"issue_id"`
		BeforeID      string `json:"before_id"`
		IncludeClosed bool   `json:"include_closed"`
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
	body.IssueID = db.NormalizeIssueID(body.IssueID)
	if body.BeforeID != "" {
		body.BeforeID = db.NormalizeIssueID(body.BeforeID)
	}
	includeClosed := body.IncludeClosed || r.URL.Query().Get("include_closed") == "true"
	issues, err := boardCandidates(ctx, board, includeClosed, body.IssueID, body.BeforeID)
	if err != nil {
		WriteError(w, ErrInternal, "could not load board tasks", http.StatusInternalServerError)
		return
	}
	if err := ctx.DB.MoveBoardIssueLogged(board.ID, body.IssueID, body.BeforeID, issues, ctx.SessionID); err != nil {
		if errors.Is(err, db.ErrBoardChanged) {
			WriteError(w, ErrConflict, "The task or the task it should be placed before is no longer on this board. Refresh the board and try again.", http.StatusConflict)
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
