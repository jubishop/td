package db

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/marcus/td/internal/models"
)

var ErrBoardChanged = errors.New("board changed: refresh before moving this task")

// MoveBoardIssueLogged anchors a move to an issue ID, including on boards whose
// tasks have no explicit positions yet. An empty beforeID means the end.
//
// candidates is the board's visible task list in query order. Positioned tasks
// sort ahead of unpositioned ones, so only unpositioned tasks that must stay
// ahead of the moved task are given positions; tasks below it keep following
// the board query. Positions of tasks outside candidates are left untouched.
func (db *DB) MoveBoardIssueLogged(boardID, issueID, beforeID string, candidates []models.Issue, sessionID string) error {
	return db.withWriteLock(func() error {
		views, err := db.ApplyBoardPositions(boardID, candidates)
		if err != nil {
			return err
		}
		maxPosition := 0
		if err := db.conn.QueryRow(`SELECT COALESCE(MAX(position), 0) FROM board_issue_positions WHERE board_id = ?`, boardID).Scan(&maxPosition); err != nil {
			return err
		}
		old := make(map[string]int)
		positions := make(map[string]int)
		var order []string
		found := false
		for _, view := range views {
			id := view.Issue.ID
			if view.HasPosition {
				old[id] = view.Position
				positions[id] = view.Position
				maxPosition = max(maxPosition, view.Position)
			}
			if id == issueID {
				found = true
				continue
			}
			order = append(order, id)
		}
		index := len(order)
		if beforeID != "" {
			index = slices.Index(order, beforeID)
		}
		if !found || index < 0 {
			return ErrBoardChanged
		}
		order = slices.Insert(order, index, issueID)
		delete(positions, issueID)

		// Everything above the moved task must be positioned to stay above it.
		for _, id := range order[:index] {
			if _, ok := positions[id]; !ok {
				maxPosition += PositionGap
				positions[id] = maxPosition
			}
		}

		renumber := false
		nextPositioned := false
		if index+1 < len(order) {
			_, nextPositioned = positions[order[index+1]]
		}
		switch {
		case index == 0 && nextPositioned:
			positions[issueID] = positions[order[1]] - PositionGap
		case index == 0:
			positions[issueID] = maxPosition + PositionGap
		case !nextPositioned:
			positions[issueID] = max(positions[order[index-1]], maxPosition) + PositionGap
		default:
			lo, hi := positions[order[index-1]], positions[order[index+1]]
			if hi-lo < 2 {
				renumber = true
			} else {
				positions[issueID] = lo + (hi-lo)/2
			}
		}
		if !renumber {
			var collisions int
			if err := db.conn.QueryRow(`SELECT COUNT(*) FROM board_issue_positions WHERE board_id = ? AND issue_id != ? AND position = ?`, boardID, issueID, positions[issueID]).Scan(&collisions); err != nil {
				return err
			}
			renumber = collisions > 0
		}
		if renumber {
			// Respace only the positioned prefix, above every existing row.
			next := maxPosition
			for _, id := range order {
				if _, ok := positions[id]; ok || id == issueID {
					next += PositionGap
					positions[id] = next
				}
			}
		}

		tx, err := db.conn.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		now := time.Now()
		for _, id := range order {
			position, ok := positions[id]
			if !ok {
				continue
			}
			if previous, exists := old[id]; exists && previous == position {
				continue
			}
			rowID := BoardIssuePosID(boardID, id)
			_, err = tx.Exec(`INSERT INTO board_issue_positions (id, board_id, issue_id, position, added_at)
				VALUES (?, ?, ?, ?, ?) ON CONFLICT(board_id, issue_id) DO UPDATE SET position = excluded.position, added_at = excluded.added_at, deleted_at = NULL`, rowID, boardID, id, position, now)
			if err != nil {
				return err
			}
			previousData := ""
			if previous, exists := old[id]; exists {
				data, _ := json.Marshal(map[string]interface{}{"id": rowID, "board_id": boardID, "issue_id": id, "position": previous})
				previousData = string(data)
			}
			data, _ := json.Marshal(map[string]interface{}{"id": rowID, "board_id": boardID, "issue_id": id, "position": position, "added_at": now.UTC().Format(time.RFC3339)})
			actionID, err := generateActionID()
			if err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO action_log (id, session_id, action_type, entity_type, entity_id, previous_data, new_data, timestamp, undone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`, actionID, sessionID, string(models.ActionBoardSetPosition), "board_issue_positions", rowID, previousData, string(data), formatActionLogTimestamp(now))
			if err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}
