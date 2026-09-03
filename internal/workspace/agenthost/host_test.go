package agenthost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSettingsValidationAndFailClosedLoading(t *testing.T) {
	if err := DefaultSettings().validate(); err != nil {
		t.Fatal(err)
	}
	// Change one field at a time so an unrelated zero value cannot mask a
	// missing validation branch.
	for name, mutate := range map[string]func(*Settings){
		"invalid interval":          func(s *Settings) { s.RefreshInterval = "oops" },
		"negative interval":         func(s *Settings) { s.RefreshInterval = "-1s" },
		"short interval":            func(s *Settings) { s.RefreshInterval = "1ms" },
		"unknown access":            func(s *Settings) { s.ProjectAccess = "unknown" },
		"no refresh workers":        func(s *Settings) { s.RefreshConcurrency = 0 },
		"excess refresh workers":    func(s *Settings) { s.RefreshConcurrency = 17 },
		"no repository workers":     func(s *Settings) { s.RepositoryWorkers = 0 },
		"excess repository workers": func(s *Settings) { s.RepositoryWorkers = 33 },
		"no job workers":            func(s *Settings) { s.JobWorkers = 0 },
		"excess job workers":        func(s *Settings) { s.JobWorkers = 17 },
		"no queue capacity":         func(s *Settings) { s.QueueCapacity = 0 },
		"excess queue capacity":     func(s *Settings) { s.QueueCapacity = 10001 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := DefaultSettings()
			mutate(&bad)
			if bad.validate() == nil {
				t.Fatal("invalid settings accepted", bad)
			}
		})
	}
	for _, body := range []string{`{}`, `{"unknown":1}`, `{"repository_workers":4,"job_workers":2,"queue_capacity":256} {}`, strings.Repeat("x", 8193)} {
		home := t.TempDir()
		os.WriteFile(filepath.Join(home, "agent-settings.json"), []byte(body), 0600)
		// {} preserves defaults and is deliberately supported for initial settings.
		_, err := New("/missing/diffmind", home)
		if body == "{}" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("bad configuration accepted")
		}
	}
	home := t.TempDir()
	os.Symlink("/missing", filepath.Join(home, "agent-settings.json"))
	if _, e := New("/missing/diffmind", home); e == nil {
		t.Fatal("symlink accepted")
	}
	home = t.TempDir()
	os.WriteFile(filepath.Join(home, "agent-settings.json"), []byte("{}"), 0644)
	if _, e := New("/missing/diffmind", home); e == nil {
		t.Fatal("public settings accepted")
	}
}
func TestLocalCommandBoundariesAndEnvironment(t *testing.T) {
	for _, args := range [][]string{nil, {"sh", "-c", "anything"}, {"run", "--repo", "/"}, {"backup", "restore"}, {"backup", "rotate"}, {"storage", "migrate"}} {
		if validateCommand(args, "") == nil {
			t.Fatal("invalid/unconfirmed command accepted", args)
		}
	}
	for _, args := range [][]string{{"doctor", "--json"}, {"pack", "test", "/tmp/fixtures"}, {"backup", "create", "--offline", "--output", "/tmp/a"}, {"backup", "rotate"}, {"storage", "migrate"}} {
		confirm := ""
		if len(args) > 1 {
			confirm = args[0] + " " + args[1]
		}
		if err := validateCommand(args, confirm); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DIFFMIND_HOME", "wrong")
	t.Setenv("DIFFMIND_BINARY", "wrong")
	t.Setenv("DIFFMIND_AUTH_TOKEN", "secret")
	t.Setenv("DIFFMIND_TRUSTED_PROXY_SECRET", "proxy")
	h, e := New("/missing/diffmind", t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	env := strings.Join(h.environment(), "\n")
	if strings.Contains(env, "secret") || strings.Contains(env, "DIFFMIND_TRUSTED_PROXY_SECRET=") || strings.Contains(env, "DIFFMIND_BINARY=wrong") {
		t.Fatal("shared-server environment inherited")
	}
	if err := h.Start(context.Background()); err == nil {
		t.Fatal("missing binary started")
	}
	if h.Status().Running {
		t.Fatal("failed backend marked running")
	}
}
func TestBoundedConcurrentOutput(t *testing.T) {
	b := &limitedBuffer{limit: 100}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.Write([]byte(strings.Repeat("x", 20))); _ = b.String() }()
	}
	wg.Wait()
	if len(b.String()) != 100 || !b.Truncated() {
		t.Fatal("output was not bounded")
	}
}
