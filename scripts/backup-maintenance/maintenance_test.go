package maintenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServiceBackupLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, state, fail string
		want              []string
		success           bool
	}{
		{"active", "active", "", []string{"stop", "backup", "start"}, true},
		{"inactive", "inactive", "", []string{"backup"}, true},
		{"backup-failed", "active", "backup", []string{"stop", "backup", "start"}, false},
		{"stop-failed", "active", "stop", []string{"stop", "start"}, false},
		{"restart-failed", "active", "start", []string{"stop", "backup", "start"}, false},
		{"inactive-backup-failed", "inactive", "backup", []string{"backup"}, false},
		{"transitioning", "activating", "", nil, false},
		{"failed", "failed", "", nil, false},
		{"missing", "active", "load", nil, false},
		{"locked", "active", "lock", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, root := maintenanceCommand(t, tc.state, tc.fail)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("exit %v: %s", err, out)
			}
			assertActions(t, root, tc.want)
		})
	}
}

func maintenanceCommand(t *testing.T, state, fail string) (*exec.Cmd, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "home with spaces", "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "backups with spaces"), 0700); err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../backup-systemd.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Fixture executables are copied to an isolated PATH; never contact the host
	// service manager, stop real services or use real backup state in these tests.
	for _, name := range []string{"systemctl", "flock", "diffmind"} {
		body, err := os.ReadFile(filepath.Join(fixtures, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), body, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_ROOT="+root, "TEST_STATE="+state, "TEST_FAIL="+fail,
		"DIFFMIND_SERVICE=diffmind-test.service", "DIFFMIND_BINARY="+filepath.Join(root, "diffmind"), "DIFFMIND_HOME="+filepath.Join(root, "home with spaces"),
		"DIFFMIND_BACKUP_DIRECTORY="+filepath.Join(root, "backups with spaces"), "DIFFMIND_BACKUP_KEEP_LAST=7", "DIFFMIND_BACKUP_LOCK="+filepath.Join(root, "rotation.lock"))
	return cmd, root
}

func assertActions(t *testing.T, root string, want []string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "actions"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != strings.Join(want, "\n") {
		t.Fatalf("actions %q want %q", body, want)
	}
}

func TestServiceBackupSignalRestartsAfterChildStops(t *testing.T) {
	cmd, root := maintenanceCommand(t, "active", "wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("signal reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backup did not stop")
	}
	assertActions(t, root, []string{"stop", "backup", "child-stopped", "start"})
}

func TestServiceBackupPostStopRecovery(t *testing.T) {
	for _, kind := range []string{"interrupted", "no-intent", "wrong-service", "restart-fails"} {
		t.Run(kind, func(t *testing.T) {
			fail := ""
			if kind == "restart-fails" {
				fail = "start"
			}
			cmd, root := maintenanceCommand(t, "inactive", fail)
			cmd.Args = append(cmd.Args, "--recover")
			marker := filepath.Join(root, "rotation.lock.restart")
			if kind != "no-intent" {
				service := "diffmind-test.service"
				if kind == "wrong-service" {
					service = "unrelated.service"
				}
				if err := os.WriteFile(marker, []byte(service+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := cmd.CombinedOutput()
			wantOK := kind == "interrupted" || kind == "no-intent"
			if (err == nil) != wantOK {
				t.Fatalf("recovery %v %s", err, out)
			}
			want := []string{}
			if kind == "interrupted" || kind == "restart-fails" {
				want = []string{"start"}
			}
			assertActions(t, root, want)
			_, statErr := os.Stat(marker)
			if wantOK && !os.IsNotExist(statErr) {
				t.Fatal("completed intent not cleared")
			}
			if !wantOK && statErr != nil {
				t.Fatal("unresolved intent lost")
			}
		})
	}
}

func TestServiceBackupRejectsUnsafeConfigurationBeforeStopping(t *testing.T) {
	for _, setting := range []string{"DIFFMIND_SERVICE=--all", "DIFFMIND_SERVICE=../other.service", "DIFFMIND_SERVICE=other", "DIFFMIND_BACKUP_KEEP_LAST=0", "DIFFMIND_BACKUP_KEEP_LAST=1001", "DIFFMIND_BACKUP_KEEP_LAST=1; false", "DIFFMIND_BINARY=relative", "DIFFMIND_BACKUP_LOCK=relative"} {
		t.Run(setting, func(t *testing.T) {
			cmd, root := maintenanceCommand(t, "active", "")
			key := strings.SplitN(setting, "=", 2)[0] + "="
			env := []string{}
			for _, e := range cmd.Env {
				if !strings.HasPrefix(e, key) {
					env = append(env, e)
				}
			}
			cmd.Env = append(env, setting)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("accepted %s: %s", setting, out)
			}
			assertActions(t, root, nil)
		})
	}
}
