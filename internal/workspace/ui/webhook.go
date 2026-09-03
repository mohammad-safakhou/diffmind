package ui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var deliveryIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func isGitHubWebhook(r *http.Request) bool {
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodPost && len(p) == 6 && p[0] == "api" && p[1] == "v1" && p[2] == "projects" && p[3] != "" && p[4] == "webhooks" && p[5] == "github"
}
func validWebhookSignature(secret, signature string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(digest) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(digest, mac.Sum(nil))
}
func repositoryOrigin(raw string) string {
	if strings.HasPrefix(raw, "git@") {
		raw = "ssh://" + strings.Replace(raw, ":", "/", 1)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return strings.ToLower(u.Host + "/" + path)
}
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	s.operationsMu.Lock()
	secret := s.operationsConfig.WebhookSecret
	s.operationsMu.Unlock()
	if secret == "" {
		writeErr(w, 404, errors.New("webhooks are disabled"))
		return
	}
	// Authenticate the raw bounded bytes before parsing or reading project data.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeErr(w, 413, errors.New("webhook payload exceeds 2 MiB or cannot be read"))
		return
	}
	if !validWebhookSignature(secret, r.Header.Get("X-Hub-Signature-256"), body) {
		writeErr(w, 401, errors.New("invalid webhook signature"))
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		writeJSON(w, 200, map[string]string{"status": "pong"})
		return
	}
	if event != "push" {
		writeJSON(w, 200, map[string]string{"status": "ignored", "reason": "only push events trigger refresh"})
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if !deliveryIDPattern.MatchString(delivery) {
		writeErr(w, 400, errors.New("valid X-GitHub-Delivery is required"))
		return
	}
	var payload struct {
		Ref        string `json:"ref"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			HTMLURL       string `json:"html_url"`
			DefaultBranch string `json:"default_branch"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, 400, errors.New("invalid webhook JSON"))
		return
	}
	origin := repositoryOrigin(payload.Repository.HTMLURL)
	if origin == "" || !strings.HasPrefix(payload.Ref, "refs/heads/") {
		writeJSON(w, 200, map[string]string{"status": "ignored", "reason": "not a repository branch push"})
		return
	}
	repos, err := s.store.ListRepos(r.PathValue("pid"))
	if err != nil {
		s.jobError(w, err)
		return
	}
	matched := false
	for _, repo := range repos {
		if repositoryOrigin(repo.GitURL) != origin {
			continue
		}
		branch := firstNonEmpty(repo.DefaultBranch, payload.Repository.DefaultBranch)
		if branch != "" && payload.Ref == "refs/heads/"+branch {
			matched = true
			break
		}
	}
	if !matched || payload.Deleted {
		writeJSON(w, 200, map[string]string{"status": "ignored", "reason": "repository or branch is not tracked, or branch was deleted"})
		return
	}
	digest := sha256.Sum256(body)
	job, duplicate, err := s.enqueueRefresh(r.PathValue("pid"), "github_push", delivery, hex.EncodeToString(digest[:]))
	if err != nil {
		s.jobError(w, err)
		return
	}
	// Do not disclose history, evidence, or credentials through the webhook path.
	writeJSON(w, 202, map[string]any{"job_id": job.ID, "status": job.Status, "duplicate": duplicate})
}
