// Package agenthost supervises an agent-owned local backend. No terminal or
// separately started server is required, and offline maintenance drains it first.
package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/agentapi"
)

type Settings struct {
	RefreshInterval    string `json:"refresh_interval"`
	RefreshOnStart     bool   `json:"refresh_on_start"`
	RefreshConcurrency int    `json:"refresh_concurrency"`
	ProjectAccess      string `json:"project_access"`
	RepositoryWorkers  int    `json:"repository_workers"`
	JobWorkers         int    `json:"job_workers"`
	QueueCapacity      int    `json:"queue_capacity"`
}

func DefaultSettings() Settings {
	return Settings{RepositoryWorkers: 4, JobWorkers: 2, QueueCapacity: 256, RefreshConcurrency: 4, ProjectAccess: "legacy"}
}
func (s Settings) validate() error {
	if s.RefreshConcurrency < 1 || s.RefreshConcurrency > 16 {
		return errors.New("refresh_concurrency must be 1..16")
	}
	if s.ProjectAccess != "legacy" && s.ProjectAccess != "scoped" {
		return errors.New("project_access must be legacy or scoped")
	}
	if s.RefreshInterval != "" && s.RefreshInterval != "0" {
		d, e := time.ParseDuration(s.RefreshInterval)
		if e != nil || d < time.Second {
			return errors.New("refresh_interval must be 0, empty, or a duration >=1s")
		}
	}
	if s.RepositoryWorkers < 1 || s.RepositoryWorkers > 32 || s.JobWorkers < 1 || s.JobWorkers > 16 || s.QueueCapacity < 1 || s.QueueCapacity > 10000 {
		return errors.New("worker/capacity limits out of range")
	}
	return nil
}

type Host struct {
	mu           sync.Mutex
	Binary, Home string
	settings     Settings
	cmd          *exec.Cmd
	done         chan error
	ownerPipe    *os.File
	url          string
	log          *limitedBuffer
	client       *http.Client
}
type Status struct {
	Running      bool     `json:"running"`
	Home         string   `json:"home"`
	DashboardURL string   `json:"dashboard_url,omitempty"`
	Settings     Settings `json:"settings"`
	Lifecycle    string   `json:"lifecycle"`
}

func New(binary, home string) (*Host, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(home) {
		return nil, errors.New("binary and home must be absolute")
	}
	h := &Host{Binary: binary, Home: home, settings: DefaultSettings(), client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("backend redirects refused") }}}
	path := filepath.Join(home, "agent-settings.json")
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
			return nil, errors.New("agent settings must be a private regular file <=8 KiB")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if e = dec.Decode(&h.settings); e != nil {
			return nil, e
		}
		var trailing any
		if e = dec.Decode(&trailing); e != io.EOF {
			return nil, errors.New("trailing agent settings data")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err = h.settings.validate(); err != nil {
		return nil, err
	}
	return h, nil
}
func (h *Host) Status() Status { h.mu.Lock(); defer h.mu.Unlock(); return h.status() }
func (h *Host) status() Status {
	if h.cmd != nil {
		select {
		case <-h.done:
			if h.ownerPipe != nil {
				h.ownerPipe.Close()
				h.ownerPipe = nil
			}
			h.cmd = nil
			h.url = ""
		default:
		}
	}
	return Status{Running: h.cmd != nil, Home: h.Home, DashboardURL: h.url, Settings: h.settings, Lifecycle: "Agent-owned: starts on connection/management, stops on disconnect. Use shared deployment for always-on refresh."}
}
func (h *Host) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.start(ctx)
}
func (h *Host) start(ctx context.Context) error {
	if h.status().Running {
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	file, err := listener.(*net.TCPListener).File()
	if err != nil {
		listener.Close()
		return err
	}
	defer file.Close()
	ownerRead, ownerWrite, err := os.Pipe()
	if err != nil {
		listener.Close()
		return err
	}
	defer ownerRead.Close()
	h.url = "http://" + listener.Addr().String()
	listener.Close()
	args := []string{"agent-service", "--no-spa-rebuild", "--refresh-interval", h.settings.RefreshInterval, "--refresh-on-start=" + strconv.FormatBool(h.settings.RefreshOnStart), "--repository-workers", strconv.Itoa(h.settings.RepositoryWorkers), "--job-workers", strconv.Itoa(h.settings.JobWorkers), "--queue-capacity", strconv.Itoa(h.settings.QueueCapacity)}
	args = append(args, "--refresh-concurrency", strconv.Itoa(h.settings.RefreshConcurrency), "--project-access", h.settings.ProjectAccess)
	cmd := exec.Command(h.Binary, args...)
	cmd.Env = h.environment()
	cmd.ExtraFiles = []*os.File{file, ownerRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	h.log = &limitedBuffer{limit: 32768}
	cmd.Stdout = h.log
	cmd.Stderr = h.log
	if err = cmd.Start(); err != nil {
		ownerWrite.Close()
		h.url = ""
		return err
	}
	h.cmd = cmd
	h.ownerPipe = ownerWrite
	h.done = make(chan error, 1)
	go func() { h.done <- cmd.Wait() }()
	readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, _ := http.NewRequestWithContext(readyCtx, "GET", h.url+"/api/v1/session", nil)
		res, e := h.client.Do(req)
		if e == nil {
			io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
			res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
		}
		select {
		case e := <-h.done:
			h.ownerPipe.Close()
			h.ownerPipe = nil
			h.cmd = nil
			h.url = ""
			return fmt.Errorf("managed backend exited: %v: %s", e, h.log.String())
		case <-readyCtx.Done():
			_ = h.stop()
			return fmt.Errorf("managed backend startup: %w", readyCtx.Err())
		case <-ticker.C:
		}
	}
}
func (h *Host) environment() []string {
	var env []string
	// Explicitly local OS-trusted mode; never inherit a shared-server secret or
	// expose it in tool output. Listener validation in agent-service enforces loopback.
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if k == "DIFFMIND_HOME" || k == "DIFFMIND_BINARY" || k == "DIFFMIND_AUTH_TOKEN" || k == "DIFFMIND_TRUSTED_PROXY_SECRET" {
			continue
		}
		env = append(env, e)
	}
	return append(env, "DIFFMIND_HOME="+h.Home, "DIFFMIND_BINARY="+h.Binary)
}
func (h *Host) Stop() error { h.mu.Lock(); defer h.mu.Unlock(); return h.stop() }
func (h *Host) stop() error {
	if !h.status().Running {
		return nil
	}
	cmd, done := h.cmd, h.done
	_ = cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	var stopErr error
	select {
	case <-done:
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		stopErr = errors.New("backend exceeded shutdown deadline and was killed; inspect interrupted work before maintenance")
	}
	h.cmd = nil
	if h.ownerPipe != nil {
		h.ownerPipe.Close()
		h.ownerPipe = nil
	}
	h.url = ""
	return stopErr
}
func (h *Host) Configure(ctx context.Context, settings Settings) error {
	if err := settings.validate(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.settings
	if err := h.stop(); err != nil {
		return err
	}
	h.settings = settings
	if err := h.start(ctx); err != nil {
		h.settings = old
		recoverCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		restart := h.start(recoverCtx)
		return errors.Join(err, restart)
	}
	b, _ := json.MarshalIndent(settings, "", "  ")
	f, err := os.CreateTemp(h.Home, ".agent-settings-")
	if err == nil {
		name := f.Name()
		defer os.Remove(name)
		_, err = f.Write(append(b, '\n'))
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err == nil {
			err = os.Rename(name, filepath.Join(h.Home, "agent-settings.json"))
		}
	}
	if err != nil {
		_ = h.stop()
		h.settings = old
		recoverCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return errors.Join(err, h.start(recoverCtx))
	}
	return nil
}
func (h *Host) Invoke(ctx context.Context, _ *mcp.CallToolRequest, req *http.Request) (agentapi.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.start(ctx); err != nil {
		return agentapi.Result{}, err
	}
	u, err := req.URL.Parse(h.url + req.URL.RequestURI())
	if err != nil {
		return agentapi.Result{}, err
	}
	req = req.Clone(ctx)
	req.URL = u
	res, err := h.client.Do(req)
	if err != nil {
		return agentapi.Result{}, fmt.Errorf("backend request failed; inspect state before retrying a mutation: %w", err)
	}
	defer res.Body.Close()
	return agentapi.Decode(res.StatusCode, res.Header, res.Body)
}

type CommandResult struct {
	ExitCode         int    `json:"exit_code"`
	Output           string `json:"output"`
	Truncated        bool   `json:"truncated"`
	BackendRestarted bool   `json:"backend_restarted"`
}

func validateCommand(args []string, confirm string) error {
	if len(args) == 0 || len(args) > 64 {
		return errors.New("expected a bounded CLI argument list")
	}
	for _, a := range args {
		if len(a) > 8192 || strings.ContainsRune(a, 0) {
			return errors.New("invalid argument")
		}
	}
	switch args[0] {
	case "doctor", "version", "pack", "backup", "storage":
	default:
		return errors.New("allowed commands: doctor, version, pack, backup, storage; use management tools for ingestion/graphs")
	}
	if len(args) > 1 && ((args[0] == "backup" && (args[1] == "restore" || args[1] == "rotate")) || (args[0] == "storage" && args[1] == "migrate")) {
		want := args[0] + " " + args[1]
		if confirm != want {
			return fmt.Errorf("repeat confirm=%q for this maintenance operation", want)
		}
	}
	return nil
}

// Command serializes maintenance, stops the owned backend, executes ONLY the
// current binary (never a shell), and restores the backend even after failure.
func (h *Host) Command(ctx context.Context, args []string, confirm string) (CommandResult, error) {
	if err := validateCommand(args, confirm); err != nil {
		return CommandResult{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	running := h.status().Running
	if err := h.stop(); err != nil {
		return CommandResult{}, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, h.Binary, args...)
	cmd.Env = h.environment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	output := &limitedBuffer{limit: 1 << 20}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	result := CommandResult{Output: output.String(), Truncated: output.Truncated()}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.ExitCode = exit.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}
	if running {
		restartCtx, done := context.WithTimeout(context.Background(), 20*time.Second)
		restartErr := h.start(restartCtx)
		done()
		result.BackendRestarted = restartErr == nil
		if restartErr != nil {
			return result, fmt.Errorf("command exit=%d; backend restart failed: %w", result.ExitCode, restartErr)
		}
	}
	return result, nil
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := b.limit - b.buf.Len()
	if n > left {
		b.truncated = true
		p = p[:left]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}
func (b *limitedBuffer) String() string  { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }
func (b *limitedBuffer) Truncated() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.truncated }
