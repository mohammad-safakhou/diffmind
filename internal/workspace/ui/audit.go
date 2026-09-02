package ui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var auditWriteMu sync.Mutex

type auditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"request_id"`
	Actor      string    `json:"actor"`
	Role       Role      `json:"role"`
	AuthMethod string    `json:"auth_method"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	ClientIP   string    `json:"client_ip,omitempty"`
}

// SetAuditLogPath overrides the JSONL audit path. It is primarily useful for
// embedding DiffMind and tests; New defaults it below DIFFMIND_HOME.
func (s *Server) SetAuditLogPath(path string) {
	s.auditLogPath = strings.TrimSpace(path)
}

func (s *Server) writeAudit(r *http.Request, requestID string, identity Identity, status int) {
	if s.auditLogPath == "" {
		return
	}
	event := auditEvent{
		Timestamp:  time.Now().UTC(),
		RequestID:  requestID,
		Actor:      identity.User,
		Role:       identity.Role,
		AuthMethod: identity.AuthMethod,
		Method:     r.Method,
		Path:       r.URL.Path,
		Status:     status,
		ClientIP:   requestClientIP(r, identity.AuthMethod == "trusted_proxy"),
	}
	line, err := json.Marshal(event)
	if err != nil {
		s.log.Error("encode HTTP audit event", "error", err.Error())
		return
	}
	auditWriteMu.Lock()
	defer auditWriteMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.auditLogPath), 0o700); err != nil {
		s.log.Error("create HTTP audit directory", "error", err.Error())
		return
	}
	f, err := os.OpenFile(s.auditLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		s.log.Error("open HTTP audit log", "error", err.Error())
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		s.log.Error("write HTTP audit event", "error", err.Error())
	}
}

func requestClientIP(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func newRequestID() string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err == nil {
		return hex.EncodeToString(data[:])
	}
	return time.Now().UTC().Format("20060102T150405.000000000")
}

func isAuditedRequest(method, path string) bool {
	if path == "/mcp" {
		return false
	}
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w}
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusRecorder) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
