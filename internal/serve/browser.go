package serve

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/marcus/td/internal/db"
	"github.com/marcus/td/internal/models"
	"github.com/marcus/td/internal/query"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed web/*
var browserAssets embed.FS

var browserMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))
var browserHTMLPolicy = func() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()
	policy.AllowElements("input")
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	policy.AllowAttrs("disabled", "checked").OnElements("input")
	return policy
}()

func (s *Server) registerBrowserRoutes() {
	s.mux.HandleFunc("POST /v1/boards/{id}/move", s.handleMoveBoardIssue)
	assets, _ := fs.Sub(browserAssets, "web")
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		page, _ := browserAssets.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	s.mux.HandleFunc("GET /v1/browser", func(w http.ResponseWriter, r *http.Request) {
		minTitle, maxTitle := titleLengthLimitsFor(s.handlerContext())
		WriteSuccess(w, map[string]interface{}{
			"name": filepath.Base(s.baseDir), "path": s.baseDir,
			"session_id":       s.sessionID,
			"title_min_length": minTitle, "title_max_length": maxTitle,
		}, http.StatusOK)
	})
	s.mux.HandleFunc("POST /v1/markdown", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			WriteError(w, ErrValidation, "invalid Markdown request", http.StatusBadRequest)
			return
		}
		var rendered bytes.Buffer
		if err := browserMarkdown.Convert([]byte(body.Text), &rendered); err != nil {
			WriteError(w, ErrInternal, "could not render Markdown", http.StatusInternalServerError)
			return
		}
		WriteSuccess(w, map[string]string{"html": browserHTMLPolicy.Sanitize(rendered.String())}, http.StatusOK)
	})
}

func (s *Server) handleMoveBoardIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueID  string `json:"issue_id"`
		BeforeID string `json:"before_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IssueID == "" {
		WriteError(w, ErrValidation, "issue_id is required", http.StatusBadRequest)
		return
	}
	board, err := s.db.ResolveBoardRef(r.PathValue("id"))
	if err != nil {
		WriteError(w, ErrNotFound, "board not found", http.StatusNotFound)
		return
	}
	var issues []models.Issue
	if board.Query == "" {
		issues, err = s.db.ListIssues(db.ListIssuesOptions{SortBy: "priority"})
	} else {
		issues, err = query.Execute(s.db, board.Query, "", query.ExecuteOptions{})
	}
	if err != nil {
		WriteError(w, ErrInternal, "could not load board tasks", http.StatusInternalServerError)
		return
	}
	body.IssueID = db.NormalizeIssueID(body.IssueID)
	if body.BeforeID != "" {
		body.BeforeID = db.NormalizeIssueID(body.BeforeID)
	}
	if err := s.db.MoveBoardIssueLogged(board.ID, body.IssueID, body.BeforeID, issues, s.sessionID); err != nil {
		if errors.Is(err, db.ErrBoardChanged) {
			WriteError(w, ErrConflict, "The task or its destination no longer matches this board. Any completed status change has been saved; refresh the board to see it.", http.StatusConflict)
			return
		}
		WriteError(w, ErrInternal, "could not reorder board", http.StatusInternalServerError)
		return
	}
	s.NotifyChange()
	WriteSuccess(w, map[string]bool{"positioned": true}, http.StatusOK)
}

func (s *Server) browserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			WriteError(w, ErrForbidden, "browser requires a loopback host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Scheme != "http" || parsed.Host != r.Host || parsed.User != nil {
				WriteError(w, ErrForbidden, "cross-origin browser request rejected", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			WriteError(w, ErrForbidden, "cross-site browser request rejected", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		next.ServeHTTP(w, r)
	})
}
