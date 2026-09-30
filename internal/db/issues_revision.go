package db

import (
	"database/sql"
	"errors"

	"github.com/marcus/td/internal/models"
)

var ErrIssueChanged = errors.New("issue changed since it was loaded")

// UpdateIssueLoggedIfUnchanged checks the complete snapshot under the same
// cross-process write lock used to persist the update and its action log.
func (db *DB) UpdateIssueLoggedIfUnchanged(issue, expected *models.Issue, sessionID string, action models.ActionType) error {
	return db.withWriteLock(func() error {
		return db.withReviewSyncTxLocked(func(tx *sql.Tx) error {
			current, err := db.scanIssueRowFrom(tx, issue.ID)
			if err != nil {
				return err
			}
			if current.DeletedAt != nil || marshalIssue(current) != marshalIssue(expected) {
				return ErrIssueChanged
			}
			return db.updateIssueAndLogFromPreviousStore(tx, issue, current, sessionID, action)
		})
	})
}
