package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

var errProjectAccessUnavailable = errors.New("project access unavailable")

// ConfigureProjectAccess is startup-only. Legacy mode retains global roles.
// Scoped mode denies all non-admin access until explicit memberships exist.
func (s *Server) ConfigureProjectAccess(mode string) error {
	if mode != "legacy" && mode != "scoped" {
		return errors.New("project access must be legacy or scoped")
	}
	s.projectAccessScoped = mode == "scoped"
	return nil
}

func (s *Server) projectRole(identity Identity, pid string) (Role, error) {
	if !store.ValidID(pid) {
		return "", store.ErrNotFound
	}
	if identity.AuthMethod == "project_token" {
		if !s.projectAccessScoped || identity.TokenProject != pid {
			return "", store.ErrNotFound
		}
		token, err := s.store.ActiveProjectToken(pid, identity.TokenID)
		if err != nil {
			return "", err
		}
		return Role(token.Role), nil
	}
	if !s.projectAccessScoped || identity.Role == RoleAdmin {
		return identity.Role, nil
	}
	a, err := s.store.GetProjectAccess(pid)
	if err != nil {
		return "", err
	}
	granted := Role(a.Members[identity.User])
	if granted == "" {
		return "", store.ErrNotFound
	}
	// Proxy role is a ceiling: a viewer can never turn a stored editor grant
	// into write access. Revocation/downgrade is checked on every request.
	if identity.Role == RoleViewer {
		return RoleViewer, nil
	}
	if identity.Role != RoleEditor {
		return "", store.ErrNotFound
	}
	return granted, nil
}

func (s *Server) queryFor(r *http.Request) *query.Service {
	identity := identityFromContext(r.Context())
	if !s.projectAccessScoped || identity.Role == RoleAdmin {
		return s.query
	}
	return query.NewWithAccess(s.store, func(pid string) error {
		_, err := s.projectRole(identity, pid)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return errProjectAccessUnavailable
		}
		return err
	})
}

// routedMux places authorization inside ServeMux routing, where decoded
// PathValues are available. Every registered route passes through this wrapper.
type routedMux struct {
	mux    *http.ServeMux
	server *Server
}

func (m routedMux) Handle(pattern string, h http.Handler) {
	m.mux.Handle(pattern, m.server.scopeControlled(h))
}
func (m routedMux) HandleFunc(pattern string, h http.HandlerFunc) { m.Handle(pattern, h) }

func (s *Server) scopeControlled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, key := range []string{"pid", "rid", "repo_id", "pack_id", "jid", "tid"} {
			if id := r.PathValue(key); id != "" && !store.ValidID(id) {
				writeErr(w, 404, store.ErrNotFound)
				return
			}
		}
		// Validate only selectors that address filesystem graph records; service
		// names and topology from/to values are not filesystem identifiers.
		for _, key := range []string{"run", "run_id"} {
			if id := r.URL.Query().Get(key); id != "" && !store.ValidID(id) {
				writeErr(w, 404, store.ErrNotFound)
				return
			}
		}
		if isGitHubWebhook(r) {
			next.ServeHTTP(w, r)
			return
		} // its own HMAC identity
		identity := identityFromContext(r.Context())
		pid := r.PathValue("pid")
		if s.projectAccessScoped && identity.Role != RoleAdmin {
			if jid := r.PathValue("jid"); jid != "" {
				job, err := s.store.GetJob(jid)
				if err != nil {
					writeErr(w, 404, store.ErrNotFound)
					return
				}
				pid = job.ProjectID
			}
			if r.URL.Path == "/api/v1/jobs" && r.URL.Query().Get("project") != "" {
				pid = r.URL.Query().Get("project")
			}
		}
		if pid != "" {
			role, err := s.projectRole(identity, pid)
			if err != nil {
				s.writeAccessError(w, err)
				return
			}
			if (strings.HasSuffix(r.Pattern, "/access") || strings.Contains(r.Pattern, "/tokens") || (strings.HasSuffix(r.Pattern, "/limits") && !readMethod(r.Method))) && identity.Role != RoleAdmin {
				writeErr(w, 403, errors.New("administrator required"))
				return
			}
			if s.projectAccessScoped && identity.Role != RoleAdmin {
				if strings.HasSuffix(r.Pattern, "/repo-suggestions") {
					writeErr(w, 403, errors.New("host discovery requires an administrator"))
					return
				}
				if !readMethod(r.Method) && (role != RoleEditor || !editorOperation(r.Pattern)) {
					writeErr(w, 403, errors.New("project operation requires an administrator or editor refresh access"))
					return
				}
			}
		} else if s.projectAccessScoped && identity.Role != RoleAdmin {
			switch r.URL.Path {
			case "/api/projects", "/api/v1/projects", "/api/v1/jobs", "/api/v1/session":
				if !readMethod(r.Method) {
					writeErr(w, 403, errors.New("administrator required"))
					return
				}
			case "/mcp": // Graph reads use queryFor; management re-enters full HTTP authorization.
			default:
				if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/metrics" {
					writeErr(w, 403, errors.New("administrator required"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
func readMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
func editorOperation(pattern string) bool {
	switch pattern {
	case "POST /api/v1/projects/{pid}/refresh-jobs", "POST /api/v1/jobs/{jid}/cancel", "POST /api/v1/jobs/{jid}/retry", "POST /api/projects/{pid}/ingestion/cancel", "POST /api/projects/{pid}/runs/{rid}/cancel":
		return true
	}
	return false
}
func (s *Server) writeAccessError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, store.ErrNotFound)
		return
	}
	writeErr(w, 503, errProjectAccessUnavailable)
}

type capabilities struct {
	Mode            string `json:"mode"`
	Role            Role   `json:"role"`
	CanRefresh      bool   `json:"can_refresh"`
	CanConfigure    bool   `json:"can_configure"`
	CanDelete       bool   `json:"can_delete"`
	CanManageAccess bool   `json:"can_manage_access"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.GetProject(r.PathValue("pid")); err != nil {
		s.writeAccessError(w, err)
		return
	}
	identity := identityFromContext(r.Context())
	role, err := s.projectRole(identity, r.PathValue("pid"))
	if err != nil {
		s.writeAccessError(w, err)
		return
	}
	mode := "legacy"
	if s.projectAccessScoped {
		mode = "scoped"
	}
	writeJSON(w, 200, capabilities{Mode: mode, Role: role, CanRefresh: role == RoleAdmin || role == RoleEditor, CanConfigure: role == RoleAdmin || (!s.projectAccessScoped && role == RoleEditor), CanDelete: role == RoleAdmin, CanManageAccess: identity.Role == RoleAdmin})
}
func (s *Server) handleGetAccess(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetProjectAccess(r.PathValue("pid"))
	if err != nil {
		s.writeAccessError(w, err)
		return
	}
	writeJSON(w, 200, a)
}
func (s *Server) handlePutAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision *int              `json:"revision"`
		Members  map[string]string `json:"members"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		writeErr(w, 400, errors.New("invalid access request"))
		return
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF || req.Revision == nil || req.Members == nil {
		writeErr(w, 400, errors.New("revision and members object required; no trailing data"))
		return
	}
	a, err := s.store.PutProjectAccess(r.PathValue("pid"), *req.Revision, req.Members)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, 409, err)
		} else if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, store.ErrNotFound)
		} else if errors.Is(err, store.ErrInvalidAccess) {
			writeErr(w, 400, err)
		} else {
			writeErr(w, 503, errors.New("access policy could not be saved; check stored policy and storage health"))
		}
		return
	}
	writeJSON(w, 200, a)
}
