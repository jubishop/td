package serve

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/marcus/td/internal/models"
)

func issueRevision(issue *models.Issue) string {
	data, _ := json.Marshal(issue)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func checkIssueRevision(w http.ResponseWriter, r *http.Request, issue *models.Issue) bool {
	if expected := r.Header.Get("If-Match"); expected != "" && strings.Trim(expected, `"`) != issueRevision(issue) {
		WriteError(w, ErrConflict, "This task changed since you opened it. The saved version is unchanged; compare it with your draft before saving again.", http.StatusConflict)
		return false
	}
	return true
}
