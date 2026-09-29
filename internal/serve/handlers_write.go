package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/marcus/td/internal/config"
	"github.com/marcus/td/internal/db"
	"github.com/marcus/td/internal/dependency"
	"github.com/marcus/td/internal/git"
	"github.com/marcus/td/internal/models"
	"github.com/marcus/td/internal/query"
)

// ============================================================================
// POST /v1/issues — Create Issue
// ============================================================================

// handleCreateIssue creates a new issue from a JSON request body.
func (s *Server) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	var body IssueCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Load configurable title length limits
	titleMin, titleMax := s.titleLengthLimits()

	// Validate
	if errs := ValidateIssueCreate(&body, titleMin, titleMax); len(errs) > 0 {
		WriteValidation(w, errs)
		return
	}

	// Normalize type and priority, apply defaults
	issueType := models.TypeTask
	if body.Type != "" {
		issueType = models.NormalizeType(body.Type)
	}

	issuePriority := models.PriorityP2
	if body.Priority != "" {
		issuePriority = models.NormalizePriority(body.Priority)
	}

	// If parent_id provided, verify it exists
	if body.ParentID != "" {
		normalizedParent := db.NormalizeIssueID(body.ParentID)
		_, err := s.db.GetIssue(normalizedParent)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				WriteError(w, ErrNotFound, fmt.Sprintf("parent issue not found: %s", body.ParentID), http.StatusNotFound)
			} else {
				slog.Error("lookup parent issue", "err", err, "parent_id", body.ParentID)
				WriteError(w, ErrInternal, "failed to verify parent issue", http.StatusInternalServerError)
			}
			return
		}
		body.ParentID = normalizedParent
	}

	// Parse nullable date fields
	var deferUntil *string
	if body.DeferUntil != "" {
		deferUntil = &body.DeferUntil
	}
	var dueDate *string
	if body.DueDate != "" {
		dueDate = &body.DueDate
	}

	// Build the issue model
	issue := &models.Issue{
		Title:          body.Title,
		Description:    body.Description,
		Type:           issueType,
		Priority:       issuePriority,
		Points:         body.Points,
		Labels:         body.Labels,
		ParentID:       body.ParentID,
		Acceptance:     body.Acceptance,
		Sprint:         body.Sprint,
		Minor:          body.Minor,
		CreatorSession: s.sessionID,
		DeferUntil:     deferUntil,
		DueDate:        dueDate,
	}

	// Capture current git branch
	gitState, _ := git.GetState()
	if gitState != nil {
		issue.CreatedBranch = gitState.Branch
	}

	// Create atomically with action log
	if err := s.db.CreateIssueLogged(issue, s.sessionID); err != nil {
		slog.Error("create issue", "err", err)
		WriteError(w, ErrInternal, "failed to create issue", http.StatusInternalServerError)
		return
	}

	// Record session action for bypass prevention
	if err := s.db.RecordSessionAction(issue.ID, s.sessionID, models.ActionSessionCreated); err != nil {
		slog.Warn("failed to record session history", "err", err)
	}

	s.NotifyChange()

	dto := IssueToDTO(issue)
	WriteSuccess(w, map[string]interface{}{"issue": dto}, http.StatusCreated)
}

// ============================================================================
// PATCH /v1/issues/{id} — Update Issue
// ============================================================================

// handleUpdateIssue applies a partial update to an existing issue.
func (s *Server) handleUpdateIssue(w http.ResponseWriter, r *http.Request) {
	issueID := r.PathValue("id")
	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}

	var body IssueUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Load configurable title length limits
	titleMin, titleMax := s.titleLengthLimits()

	// Validate provided fields
	if errs := ValidateIssueUpdate(&body, titleMin, titleMax); len(errs) > 0 {
		WriteValidation(w, errs)
		return
	}

	// Fetch existing issue
	issue, err := s.db.GetIssue(issueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", issueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for update", "err", err, "id", issueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}

	if !checkIssueRevision(w, r, issue) {
		return
	}
	previous := *issue

	// Apply only non-nil fields
	if body.Title != nil {
		issue.Title = *body.Title
	}
	if body.Description != nil {
		issue.Description = *body.Description
	}
	if body.Acceptance != nil {
		issue.Acceptance = *body.Acceptance
	}
	if body.Type != nil && *body.Type != "" {
		issue.Type = models.NormalizeType(*body.Type)
	}
	if body.Priority != nil && *body.Priority != "" {
		issue.Priority = models.NormalizePriority(*body.Priority)
	}
	if body.Points != nil {
		issue.Points = *body.Points
	}
	if body.Labels != nil {
		issue.Labels = body.Labels
	}
	if body.ParentID != nil {
		parentID := *body.ParentID
		if parentID != "" {
			// Verify parent exists
			normalizedParent := db.NormalizeIssueID(parentID)
			_, err := s.db.GetIssue(normalizedParent)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					WriteError(w, ErrNotFound, fmt.Sprintf("parent issue not found: %s", parentID), http.StatusNotFound)
				} else {
					slog.Error("lookup parent issue", "err", err, "parent_id", parentID)
					WriteError(w, ErrInternal, "failed to verify parent issue", http.StatusInternalServerError)
				}
				return
			}
			seen := map[string]bool{issue.ID: true}
			for ancestorID := normalizedParent; ancestorID != ""; {
				if seen[ancestorID] {
					WriteError(w, ErrValidation, "parent task would create a cycle", http.StatusBadRequest)
					return
				}
				seen[ancestorID] = true
				ancestor, err := s.db.GetIssue(ancestorID)
				if err != nil {
					WriteError(w, ErrValidation, "could not resolve parent task", http.StatusBadRequest)
					return
				}
				ancestorID = ancestor.ParentID
			}
			issue.ParentID = normalizedParent
		} else {
			issue.ParentID = ""
		}
	}
	if body.Sprint != nil {
		issue.Sprint = *body.Sprint
	}
	if body.Minor != nil {
		issue.Minor = *body.Minor
	}
	if body.DeferUntil != nil {
		if *body.DeferUntil == "" {
			issue.DeferUntil = nil
		} else {
			issue.DeferUntil = body.DeferUntil
		}
	}
	if body.DueDate != nil {
		if *body.DueDate == "" {
			issue.DueDate = nil
		} else {
			issue.DueDate = body.DueDate
		}
	}

	// Update atomically with action log
	if err := s.db.UpdateIssueLoggedIfUnchanged(issue, &previous, s.sessionID, models.ActionUpdate); err != nil {
		if errors.Is(err, db.ErrIssueChanged) {
			WriteError(w, ErrConflict, "Task changed while saving. Compare the saved version with your draft.", http.StatusConflict)
			return
		}
		slog.Error("update issue", "err", err, "id", issueID)
		WriteError(w, ErrInternal, "failed to update issue", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	dto := IssueToDTO(issue)
	WriteSuccess(w, map[string]interface{}{"issue": dto}, http.StatusOK)
}

// ============================================================================
// DELETE /v1/issues/{id} — Soft Delete
// ============================================================================

// handleDeleteIssue soft-deletes an issue.
func (s *Server) handleDeleteIssue(w http.ResponseWriter, r *http.Request) {
	issueID := r.PathValue("id")
	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}

	// Verify issue exists
	issue, err := s.db.GetIssue(issueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", issueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for delete", "err", err, "id", issueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}

	// Soft delete with action log
	if !checkIssueRevision(w, r, issue) {
		return
	}
	if err := s.db.DeleteIssueLoggedIfUnchanged(issue.ID, s.sessionID, issue); err != nil {
		if errors.Is(err, db.ErrIssueChanged) {
			WriteError(w, ErrConflict, "Task changed before deletion. Refresh and review it again.", http.StatusConflict)
			return
		}
		slog.Error("delete issue", "err", err, "id", issue.ID)
		WriteError(w, ErrInternal, "failed to delete issue", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	WriteSuccess(w, map[string]interface{}{"deleted": true}, http.StatusOK)
}

// ============================================================================
// POST /v1/boards — Create Board
// ============================================================================

// BoardCreateBody represents the expected JSON body for creating a board.
type BoardCreateBody struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

// handleCreateBoard creates a new board from a JSON request body.
func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	var body BoardCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate: name is required
	if strings.TrimSpace(body.Name) == "" {
		WriteValidation(w, []FieldError{{
			Field:   "name",
			Rule:    "required",
			Message: "name is required",
		}})
		return
	}

	// Validate: query must parse as TDQ if provided
	if body.Query != "" {
		if _, err := query.Parse(body.Query); err != nil {
			WriteValidation(w, []FieldError{{
				Field:   "query",
				Rule:    "tdq_syntax",
				Value:   body.Query,
				Message: "invalid TDQ query: " + err.Error(),
			}})
			return
		}
	}

	board, err := s.db.CreateBoardLogged(body.Name, body.Query, s.sessionID)
	if err != nil {
		slog.Error("create board", "err", err)
		WriteError(w, ErrInternal, "failed to create board", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	dto := BoardToDTO(board)
	WriteSuccess(w, map[string]interface{}{"board": dto}, http.StatusCreated)
}

// ============================================================================
// PATCH /v1/boards/{id} — Update Board
// ============================================================================

// BoardUpdateBody represents the expected JSON body for updating a board.
// All fields are optional; only present fields are applied.
type BoardUpdateBody struct {
	Name  *string `json:"name"`
	Query *string `json:"query"`
}

// handleUpdateBoard applies a partial update to an existing board.
func (s *Server) handleUpdateBoard(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("id")
	if boardID == "" {
		WriteError(w, ErrValidation, "board id is required", http.StatusBadRequest)
		return
	}

	var body BoardUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Resolve board by ID or name
	board, err := s.db.ResolveBoardRef(boardID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("board not found: %s", boardID), http.StatusNotFound)
		} else {
			slog.Error("get board for update", "err", err, "id", boardID)
			WriteError(w, ErrInternal, "failed to fetch board", http.StatusInternalServerError)
		}
		return
	}

	// Cannot modify builtin boards
	if board.IsBuiltin {
		WriteError(w, ErrForbidden, "cannot modify builtin board", http.StatusForbidden)
		return
	}

	// Validate query if provided
	if body.Query != nil && *body.Query != "" {
		if _, err := query.Parse(*body.Query); err != nil {
			WriteValidation(w, []FieldError{{
				Field:   "query",
				Rule:    "tdq_syntax",
				Value:   *body.Query,
				Message: "invalid TDQ query: " + err.Error(),
			}})
			return
		}
	}

	// Apply only provided fields
	if body.Name != nil {
		if strings.TrimSpace(*body.Name) == "" {
			WriteValidation(w, []FieldError{{
				Field:   "name",
				Rule:    "required",
				Message: "name cannot be empty",
			}})
			return
		}
		board.Name = *body.Name
	}
	if body.Query != nil {
		board.Query = *body.Query
	}

	if err := s.db.UpdateBoardLogged(board, s.sessionID); err != nil {
		slog.Error("update board", "err", err, "id", boardID)
		WriteError(w, ErrInternal, "failed to update board", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	dto := BoardToDTO(board)
	WriteSuccess(w, map[string]interface{}{"board": dto}, http.StatusOK)
}

// ============================================================================
// DELETE /v1/boards/{id} — Delete Board
// ============================================================================

// handleDeleteBoard deletes a board.
func (s *Server) handleDeleteBoard(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("id")
	if boardID == "" {
		WriteError(w, ErrValidation, "board id is required", http.StatusBadRequest)
		return
	}

	// Resolve board by ID or name
	board, err := s.db.ResolveBoardRef(boardID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("board not found: %s", boardID), http.StatusNotFound)
		} else {
			slog.Error("get board for delete", "err", err, "id", boardID)
			WriteError(w, ErrInternal, "failed to fetch board", http.StatusInternalServerError)
		}
		return
	}

	// Cannot delete builtin boards
	if board.IsBuiltin {
		WriteError(w, ErrForbidden, "cannot delete builtin board", http.StatusForbidden)
		return
	}

	if err := s.db.DeleteBoardLogged(board.ID, s.sessionID); err != nil {
		slog.Error("delete board", "err", err, "id", boardID)
		WriteError(w, ErrInternal, "failed to delete board", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	WriteSuccess(w, map[string]interface{}{"deleted": true}, http.StatusOK)
}

// ============================================================================
// POST /v1/boards/{id}/issues — Set Board Position
// ============================================================================

// BoardPositionBody represents the expected JSON body for setting an issue position on a board.
type BoardPositionBody struct {
	IssueID  string `json:"issue_id"`
	Position int    `json:"position"`
}

// handleSetBoardPosition sets or updates an issue's position on a board.
func (s *Server) handleSetBoardPosition(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("id")
	if boardID == "" {
		WriteError(w, ErrValidation, "board id is required", http.StatusBadRequest)
		return
	}

	var body BoardPositionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate: issue_id is required
	if body.IssueID == "" {
		WriteValidation(w, []FieldError{{
			Field:   "issue_id",
			Rule:    "required",
			Message: "issue_id is required",
		}})
		return
	}

	// Verify board exists
	board, err := s.db.ResolveBoardRef(boardID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("board not found: %s", boardID), http.StatusNotFound)
		} else {
			slog.Error("get board for position", "err", err, "id", boardID)
			WriteError(w, ErrInternal, "failed to fetch board", http.StatusInternalServerError)
		}
		return
	}

	// Verify issue exists
	normalizedIssueID := db.NormalizeIssueID(body.IssueID)
	_, err = s.db.GetIssue(normalizedIssueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", body.IssueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for board position", "err", err, "issue_id", body.IssueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}

	// Compute sort key from the position slot
	sortKey, _, err := s.db.ComputeInsertPosition(board.ID, body.Position)
	if err != nil {
		slog.Error("compute insert position", "err", err, "board_id", board.ID, "position", body.Position)
		WriteError(w, ErrInternal, "failed to compute position", http.StatusInternalServerError)
		return
	}

	// Set the position
	if err := s.db.SetIssuePositionLogged(board.ID, normalizedIssueID, sortKey, s.sessionID); err != nil {
		slog.Error("set board position", "err", err, "board_id", board.ID, "issue_id", normalizedIssueID)
		WriteError(w, ErrInternal, "failed to set position", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	WriteSuccess(w, map[string]interface{}{"positioned": true}, http.StatusOK)
}

// ============================================================================
// DELETE /v1/boards/{id}/issues/{issue_id} — Remove Board Position
// ============================================================================

// handleRemoveBoardPosition removes an issue's explicit position from a board.
func (s *Server) handleRemoveBoardPosition(w http.ResponseWriter, r *http.Request) {
	boardID := r.PathValue("id")
	issueID := r.PathValue("issue_id")

	if boardID == "" {
		WriteError(w, ErrValidation, "board id is required", http.StatusBadRequest)
		return
	}
	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}

	// Verify board exists
	board, err := s.db.ResolveBoardRef(boardID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("board not found: %s", boardID), http.StatusNotFound)
		} else {
			slog.Error("get board for position removal", "err", err, "id", boardID)
			WriteError(w, ErrInternal, "failed to fetch board", http.StatusInternalServerError)
		}
		return
	}

	normalizedIssueID := db.NormalizeIssueID(issueID)

	if err := s.db.RemoveIssuePositionLogged(board.ID, normalizedIssueID, s.sessionID); err != nil {
		if strings.Contains(err.Error(), "not positioned") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue %s not positioned on board %s", issueID, boardID), http.StatusNotFound)
		} else {
			slog.Error("remove board position", "err", err, "board_id", board.ID, "issue_id", normalizedIssueID)
			WriteError(w, ErrInternal, "failed to remove position", http.StatusInternalServerError)
		}
		return
	}

	s.NotifyChange()

	WriteSuccess(w, map[string]interface{}{"removed": true}, http.StatusOK)
}

// ============================================================================
// POST /v1/issues/{id}/comments — Add Comment
// ============================================================================

// CommentCreateBody represents the expected JSON body for adding a comment.
type CommentCreateBody struct {
	Text string `json:"text"`
}

// handleAddComment adds a comment to an issue.
func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	issueID := r.PathValue("id")
	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}

	var body CommentCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate: text must not be empty
	if strings.TrimSpace(body.Text) == "" {
		WriteValidation(w, []FieldError{{
			Field:   "text",
			Rule:    "required",
			Message: "text is required",
		}})
		return
	}

	// Verify issue exists
	issue, err := s.db.GetIssue(issueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", issueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for comment", "err", err, "id", issueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}

	comment := &models.Comment{
		IssueID:   issue.ID,
		SessionID: s.sessionID,
		Text:      body.Text,
	}

	if err := s.db.AddComment(comment); err != nil {
		slog.Error("add comment", "err", err, "issue_id", issue.ID)
		WriteError(w, ErrInternal, "failed to add comment", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	dto := CommentToDTO(comment)
	WriteSuccess(w, map[string]interface{}{"comment": dto}, http.StatusCreated)
}

// ============================================================================
// DELETE /v1/issues/{id}/comments/{comment_id} — Delete Comment
// ============================================================================

// handleDeleteComment deletes a comment from an issue.
func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	issueID := db.NormalizeIssueID(r.PathValue("id"))
	commentID := r.PathValue("comment_id")

	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}
	if commentID == "" {
		WriteError(w, ErrValidation, "comment id is required", http.StatusBadRequest)
		return
	}

	// Look up the comment and verify it belongs to this issue
	comment, err := s.db.GetCommentByID(commentID)
	if err != nil {
		slog.Error("get comment for delete", "err", err, "comment_id", commentID)
		WriteError(w, ErrInternal, "failed to fetch comment", http.StatusInternalServerError)
		return
	}
	if comment == nil {
		WriteError(w, ErrNotFound, fmt.Sprintf("comment not found: %s", commentID), http.StatusNotFound)
		return
	}
	if comment.IssueID != issueID {
		WriteError(w, ErrNotFound, fmt.Sprintf("comment %s not found on issue %s", commentID, issueID), http.StatusNotFound)
		return
	}

	// Hard-delete with action log
	if err := s.db.DeleteCommentLogged(commentID, s.sessionID); err != nil {
		slog.Error("delete comment", "err", err, "comment_id", commentID)
		WriteError(w, ErrInternal, "failed to delete comment", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	WriteSuccess(w, map[string]interface{}{"deleted": true}, http.StatusOK)
}

// ============================================================================
// POST /v1/issues/{id}/dependencies — Add Dependency
// ============================================================================

// DependencyCreateBody represents the expected JSON body for adding a dependency.
type DependencyCreateBody struct {
	DependsOn string `json:"depends_on"`
}

// handleAddDependency adds a dependency between two issues.
func (s *Server) handleAddDependency(w http.ResponseWriter, r *http.Request) {
	requestedIssueID := r.PathValue("id")
	if requestedIssueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}

	var body DependencyCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if body.DependsOn == "" {
		WriteValidation(w, []FieldError{{
			Field:   "depends_on",
			Rule:    "required",
			Message: "depends_on is required",
		}})
		return
	}

	issue, err := s.db.GetIssue(requestedIssueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", requestedIssueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for dependency", "err", err, "id", requestedIssueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}
	issueID := issue.ID
	dependsOnID := db.NormalizeIssueID(body.DependsOn)

	// Validate both issues exist, check for cycles and duplicates
	if err := dependency.Validate(s.db, issueID, dependsOnID); err != nil {
		if err == dependency.ErrDependencyExists {
			WriteError(w, ErrConflict, "dependency already exists", http.StatusConflict)
			return
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			WriteError(w, ErrNotFound, errMsg, http.StatusNotFound)
			return
		}
		if strings.Contains(errMsg, "circular") {
			WriteError(w, ErrValidation, errMsg, http.StatusBadRequest)
			return
		}
		slog.Error("validate dependency", "err", err, "issue_id", issueID, "depends_on", dependsOnID)
		WriteError(w, ErrInternal, "failed to validate dependency", http.StatusInternalServerError)
		return
	}

	// Add the dependency with action log
	if err := s.db.AddDependencyLogged(issueID, dependsOnID, "depends_on", s.sessionID); err != nil {
		slog.Error("add dependency", "err", err, "issue_id", issueID, "depends_on", dependsOnID)
		WriteError(w, ErrInternal, "failed to add dependency", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	depID := db.DependencyID(issueID, dependsOnID, "depends_on")
	dto := DependencyDTO{
		DepID:        depID,
		IssueID:      issueID,
		DependsOnID:  dependsOnID,
		RelationType: "depends_on",
	}

	WriteSuccess(w, map[string]interface{}{"dependency": dto}, http.StatusCreated)
}

// ============================================================================
// DELETE /v1/issues/{id}/dependencies/{dep_id} — Remove Dependency
// ============================================================================

// handleDeleteDependency removes a dependency by its dep_id.
func (s *Server) handleDeleteDependency(w http.ResponseWriter, r *http.Request) {
	issueID := db.NormalizeIssueID(r.PathValue("id"))
	depID := r.PathValue("dep_id")

	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}
	if depID == "" {
		WriteError(w, ErrValidation, "dependency id is required", http.StatusBadRequest)
		return
	}

	// Look up the dependency by its deterministic dep_id
	dep, err := s.db.GetDependencyByDepID(depID)
	if err != nil {
		slog.Error("get dependency for delete", "err", err, "dep_id", depID)
		WriteError(w, ErrInternal, "failed to fetch dependency", http.StatusInternalServerError)
		return
	}
	if dep == nil {
		WriteError(w, ErrNotFound, fmt.Sprintf("dependency not found: %s", depID), http.StatusNotFound)
		return
	}

	// Verify the dependency belongs to the issue in the URL
	if dep.IssueID != issueID {
		WriteError(w, ErrNotFound, fmt.Sprintf("dependency %s not found on issue %s", depID, issueID), http.StatusNotFound)
		return
	}

	// Remove with action log
	if err := s.db.RemoveDependencyLogged(dep.IssueID, dep.DependsOnID, s.sessionID); err != nil {
		slog.Error("remove dependency", "err", err, "dep_id", depID)
		WriteError(w, ErrInternal, "failed to remove dependency", http.StatusInternalServerError)
		return
	}

	s.NotifyChange()

	WriteSuccess(w, map[string]interface{}{"removed": true}, http.StatusOK)
}

// ============================================================================
// PUT /v1/focus — Set or Clear Focused Issue
// ============================================================================

// FocusBody represents the expected JSON body for setting focus.
// IssueID is a pointer so that null/absent can be distinguished from empty string.
type FocusBody struct {
	IssueID *string `json:"issue_id"`
}

// handleSetFocus sets or clears the focused issue via config.json.
func (s *Server) handleSetFocus(w http.ResponseWriter, r *http.Request) {
	var body FocusBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, ErrValidation, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if body.IssueID == nil || *body.IssueID == "" {
		// Clear focus
		if err := config.ClearFocus(s.baseDir); err != nil {
			slog.Error("clear focus", "err", err)
			WriteError(w, ErrInternal, "failed to clear focus", http.StatusInternalServerError)
			return
		}
		WriteSuccess(w, map[string]interface{}{"focused_issue_id": nil}, http.StatusOK)
		return
	}

	// Set focus — verify issue exists first
	issueID := *body.IssueID
	issue, err := s.db.GetIssue(issueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", issueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for focus", "err", err, "id", issueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}

	if err := config.SetFocus(s.baseDir, issue.ID); err != nil {
		slog.Error("set focus", "err", err, "issue_id", issue.ID)
		WriteError(w, ErrInternal, "failed to set focus", http.StatusInternalServerError)
		return
	}

	// Do NOT trigger sync/NotifyChange for focus changes
	WriteSuccess(w, map[string]interface{}{"focused_issue_id": issue.ID}, http.StatusOK)
}

// ============================================================================
// Helpers
// ============================================================================

// titleLengthLimits returns the configured or default title length limits.
func (s *Server) titleLengthLimits() (min, max int) {
	min, max, _ = config.GetTitleLengthLimits(s.baseDir)
	return min, max
}
