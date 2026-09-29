package db

import (
	"errors"

	"github.com/marcus/td/internal/models"
)

var ErrIssueChanged = errors.New("issue changed since it was loaded")

// UpdateIssueLoggedIfUnchanged checks the complete snapshot under the same
// cross-process write lock used to persist the update and its action log.
func (db *DB) UpdateIssueLoggedIfUnchanged(issue, expected *models.Issue, sessionID string, action models.ActionType) error {
	return db.withWriteLock(func() error {
		current, err := db.scanIssueRow(issue.ID)
		if err != nil {
			return err
		}
		if current.DeletedAt != nil || marshalIssue(current) != marshalIssue(expected) {
			return ErrIssueChanged
		}
		return db.updateIssueAndLogFromPrevious(issue, current, sessionID, action)
	})
}
