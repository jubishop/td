package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/marcus/td/internal/db"
	"github.com/marcus/td/internal/models"
	"github.com/marcus/td/internal/workflow"
)

// ============================================================================
// Status Transition Endpoints
// ============================================================================

// transitionReasonBody is the optional request body for transition endpoints.
type transitionReasonBody struct {
	Reason string `json:"reason"`
}

// transitionCascadeResult holds the results of cascade operations for the response.
type transitionCascadeResult struct {
	ParentStatusUpdates []IssueDTO `json:"parent_status_updates"`
	AutoUnblocked       []IssueDTO `json:"auto_unblocked"`
}

// transitionSpec defines a status transition's configuration.
type transitionSpec struct {
	// validFrom is the set of statuses the issue may currently be in.
	validFrom []models.Status
	// toStatus is the target status.
	toStatus models.Status
	// actionType is the action_log type for the transition.
	actionType models.ActionType
	// applySideEffects mutates the issue model for transition-specific side
	// effects (session fields, closed_at, etc.). Called after status is set.
	applySideEffects func(s *Server, issue *models.Issue)
	// runCascades executes any post-transition cascades and returns results.
	runCascades func(s *Server, issue *models.Issue) transitionCascadeResult
	// defaultLogMsg is the default progress log message when no reason is given.
	defaultLogMsg string
	// logType overrides the log type (defaults to LogTypeProgress).
	logType models.LogType
}

// handleTransition is the common handler for all status transition endpoints.
func (s *Server) handleTransition(w http.ResponseWriter, r *http.Request, spec transitionSpec) {
	issueID := r.PathValue("id")
	if issueID == "" {
		WriteError(w, ErrValidation, "issue id is required", http.StatusBadRequest)
		return
	}

	// Fetch issue
	issue, err := s.db.GetIssue(issueID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, ErrNotFound, fmt.Sprintf("issue not found: %s", issueID), http.StatusNotFound)
		} else {
			slog.Error("get issue for transition", "err", err, "id", issueID)
			WriteError(w, ErrInternal, "failed to fetch issue", http.StatusInternalServerError)
		}
		return
	}
	canonicalIssueID := issue.ID
	if !checkIssueRevision(w, r, issue) {
		return
	}
	previous := *issue

	// Validate current status against allowed "from" statuses using state machine
	sm := workflow.DefaultMachine()
	if !sm.IsValidTransition(issue.Status, spec.toStatus) {
		WriteError(w, ErrConflict,
			fmt.Sprintf("cannot transition %s from %s to %s", canonicalIssueID, issue.Status, spec.toStatus),
			http.StatusConflict)
		return
	}

	// Also validate against the spec's validFrom list (which may be more
	// restrictive than the state machine for certain endpoints like approve/reject)
	if !statusIn(issue.Status, spec.validFrom) {
		WriteError(w, ErrConflict,
			fmt.Sprintf("cannot transition %s from %s to %s", canonicalIssueID, issue.Status, spec.toStatus),
			http.StatusConflict)
		return
	}

	// Parse optional reason body (body may be empty or absent)
	var reason string
	if r.Body != nil {
		bodyBytes, readErr := io.ReadAll(r.Body)
		if readErr == nil && len(bodyBytes) > 0 {
			var body transitionReasonBody
			if jsonErr := json.Unmarshal(bodyBytes, &body); jsonErr == nil {
				reason = body.Reason
			}
		}
	}

	// Apply the transition
	issue.Status = spec.toStatus
	if spec.applySideEffects != nil {
		spec.applySideEffects(s, issue)
	}

	// Persist
	if err := s.db.UpdateIssueLoggedIfUnchanged(issue, &previous, s.sessionID, spec.actionType); err != nil {
		if errors.Is(err, db.ErrIssueChanged) {
			WriteError(w, ErrConflict, "Task changed before this action could be applied. Refresh and review it again.", http.StatusConflict)
			return
		}
		slog.Error("transition issue", "err", err, "id", issueID, "to", spec.toStatus)
		WriteError(w, ErrInternal, "failed to update issue", http.StatusInternalServerError)
		return
	}

	// Log reason or default message
	logMsg := spec.defaultLogMsg
	if reason != "" {
		logMsg = reason
	}
	logType := models.LogTypeProgress
	if spec.logType != "" {
		logType = spec.logType
	}
	if logErr := s.db.AddLog(&models.Log{
		IssueID:   canonicalIssueID,
		SessionID: s.sessionID,
		Message:   logMsg,
		Type:      logType,
	}); logErr != nil {
		slog.Warn("failed to add transition log", "err", logErr, "id", canonicalIssueID)
	}

	// Run cascades
	var cascades transitionCascadeResult
	if spec.runCascades != nil {
		cascades = spec.runCascades(s, issue)
	}
	if cascades.ParentStatusUpdates == nil {
		cascades.ParentStatusUpdates = []IssueDTO{}
	}
	if cascades.AutoUnblocked == nil {
		cascades.AutoUnblocked = []IssueDTO{}
	}

	// Re-read the issue to get the final state (UpdatedAt, etc.)
	updated, err := s.db.GetIssue(canonicalIssueID)
	if err != nil {
		// Fallback to the in-memory version
		updated = issue
	}

	dto := IssueToDTO(updated)
	s.NotifyChange()
	WriteSuccess(w, map[string]interface{}{
		"issue":    dto,
		"cascades": cascades,
	}, http.StatusOK)
}

// statusIn checks if a status is in the given set.
func statusIn(s models.Status, set []models.Status) bool {
	for _, v := range set {
		if s == v {
			return true
		}
	}
	return false
}

// cascadeIDsToIssueDTOs fetches issues by ID and converts to DTOs.
func (s *Server) cascadeIDsToIssueDTOs(ids []string) []IssueDTO {
	var dtos []IssueDTO
	for _, id := range ids {
		issue, err := s.db.GetIssue(id)
		if err == nil {
			dtos = append(dtos, IssueToDTO(issue))
		}
	}
	if dtos == nil {
		return []IssueDTO{}
	}
	return dtos
}

// ============================================================================
// POST /v1/issues/{id}/start
// ============================================================================

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:  []models.Status{models.StatusOpen},
		toStatus:   models.StatusInProgress,
		actionType: models.ActionStart,
		applySideEffects: func(srv *Server, issue *models.Issue) {
			issue.ImplementerSession = srv.sessionID
		},
		defaultLogMsg: "Started work",
	})
}

// ============================================================================
// POST /v1/issues/{id}/review
// ============================================================================

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:  []models.Status{models.StatusOpen, models.StatusInProgress},
		toStatus:   models.StatusInReview,
		actionType: models.ActionReview,
		applySideEffects: func(srv *Server, issue *models.Issue) {
			if issue.ImplementerSession == "" {
				issue.ImplementerSession = srv.sessionID
			}
		},
		runCascades: func(srv *Server, issue *models.Issue) transitionCascadeResult {
			var cr transitionCascadeResult
			// Parent cascade to in_review when all siblings qualify
			if _, ids := srv.db.CascadeUpParentStatus(issue.ID, models.StatusInReview, srv.sessionID); len(ids) > 0 {
				cr.ParentStatusUpdates = srv.cascadeIDsToIssueDTOs(ids)
			}
			return cr
		},
		defaultLogMsg: "Submitted for review",
	})
}

// ============================================================================
// POST /v1/issues/{id}/approve
// ============================================================================

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:  []models.Status{models.StatusInReview},
		toStatus:   models.StatusClosed,
		actionType: models.ActionApprove,
		applySideEffects: func(srv *Server, issue *models.Issue) {
			issue.ReviewerSession = srv.sessionID
			now := time.Now()
			issue.ClosedAt = &now
		},
		runCascades: func(srv *Server, issue *models.Issue) transitionCascadeResult {
			var cr transitionCascadeResult
			// Parent cascade to closed when all siblings closed
			if _, ids := srv.db.CascadeUpParentStatus(issue.ID, models.StatusClosed, srv.sessionID); len(ids) > 0 {
				cr.ParentStatusUpdates = srv.cascadeIDsToIssueDTOs(ids)
			}
			// Dependency unblocking cascade
			if _, ids := srv.db.CascadeUnblockDependents(issue.ID, srv.sessionID); len(ids) > 0 {
				cr.AutoUnblocked = srv.cascadeIDsToIssueDTOs(ids)
			}
			return cr
		},
		defaultLogMsg: "Approved",
	})
}

// ============================================================================
// POST /v1/issues/{id}/reject
// ============================================================================

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:  []models.Status{models.StatusInReview},
		toStatus:   models.StatusOpen,
		actionType: models.ActionReject,
		applySideEffects: func(_ *Server, issue *models.Issue) {
			issue.ImplementerSession = ""
			issue.ReviewerSession = ""
			issue.ClosedAt = nil
		},
		defaultLogMsg: "Rejected",
	})
}

// ============================================================================
// POST /v1/issues/{id}/block
// ============================================================================

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:     []models.Status{models.StatusOpen, models.StatusInProgress},
		toStatus:      models.StatusBlocked,
		actionType:    models.ActionBlock,
		defaultLogMsg: "Blocked",
		logType:       models.LogTypeBlocker,
	})
}

// ============================================================================
// POST /v1/issues/{id}/unblock
// ============================================================================

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:     []models.Status{models.StatusBlocked},
		toStatus:      models.StatusOpen,
		actionType:    models.ActionUnblock,
		defaultLogMsg: "Unblocked",
	})
}

// ============================================================================
// POST /v1/issues/{id}/close
// ============================================================================

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:  []models.Status{models.StatusOpen, models.StatusInProgress, models.StatusBlocked, models.StatusInReview},
		toStatus:   models.StatusClosed,
		actionType: models.ActionClose,
		applySideEffects: func(_ *Server, issue *models.Issue) {
			now := time.Now()
			issue.ClosedAt = &now
		},
		runCascades: func(srv *Server, issue *models.Issue) transitionCascadeResult {
			var cr transitionCascadeResult
			// Parent cascade to closed when all siblings closed
			if _, ids := srv.db.CascadeUpParentStatus(issue.ID, models.StatusClosed, srv.sessionID); len(ids) > 0 {
				cr.ParentStatusUpdates = srv.cascadeIDsToIssueDTOs(ids)
			}
			// Dependency unblocking cascade
			if _, ids := srv.db.CascadeUnblockDependents(issue.ID, srv.sessionID); len(ids) > 0 {
				cr.AutoUnblocked = srv.cascadeIDsToIssueDTOs(ids)
			}
			return cr
		},
		defaultLogMsg: "Closed",
	})
}

// ============================================================================
// POST /v1/issues/{id}/reopen
// ============================================================================

func (s *Server) handleReopen(w http.ResponseWriter, r *http.Request) {
	s.handleTransition(w, r, transitionSpec{
		validFrom:  []models.Status{models.StatusClosed},
		toStatus:   models.StatusOpen,
		actionType: models.ActionReopen,
		applySideEffects: func(_ *Server, issue *models.Issue) {
			issue.ReviewerSession = ""
			issue.ClosedAt = nil
		},
		defaultLogMsg: "Reopened",
	})
}
