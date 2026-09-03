//go:build darwin || linux

package homelock

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLeaseProcess(t *testing.T) {
	if os.Getenv("DIFFMIND_LEASE_TEST") != "1" {
		return
	}
	var release func()
	var err error
	if os.Getenv("DIFFMIND_LEASE_SERVER") == "1" {
		release, err = AcquireServer(os.Getenv("DIFFMIND_LEASE_HOME"))
	} else {
		release, err = Acquire(os.Getenv("DIFFMIND_LEASE_HOME"), os.Getenv("DIFFMIND_LEASE_EXCLUSIVE") == "1")
	}
	if err != nil {
		os.Exit(3)
	}
	release()
	os.Exit(0)
}

func TestSingleServerLeaseDoesNotBlockAnalyzer(t *testing.T) {
	home := t.TempDir()
	release, err := AcquireServer(home)
	if err != nil {
		t.Fatal(err)
	}
	check := func(server, success bool) {
		t.Helper()
		value := "0"
		if server {
			value = "1"
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseProcess$")
		cmd.Env = append(os.Environ(), "DIFFMIND_LEASE_TEST=1", "DIFFMIND_LEASE_HOME="+home, "DIFFMIND_LEASE_SERVER="+value)
		err := cmd.Run()
		if (err == nil) != success {
			t.Fatalf("server=%v want success=%v err=%v", server, success, err)
		}
	}
	check(true, false)
	check(false, true)
	release()
	check(true, true)
}
func TestCrossProcessLeases(t *testing.T) {
	home := t.TempDir()
	check := func(exclusive, success bool) {
		t.Helper()
		value := "0"
		if exclusive {
			value = "1"
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseProcess$")
		cmd.Env = append(os.Environ(), "DIFFMIND_LEASE_TEST=1", "DIFFMIND_LEASE_HOME="+home, "DIFFMIND_LEASE_EXCLUSIVE="+value)
		err := cmd.Run()
		if (err == nil) != success {
			t.Fatalf("exclusive=%v success=%v err=%v", exclusive, success, err)
		}
	}
	r, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	check(false, true)
	check(true, false)
	r()
	r, err = Acquire(home, true)
	if err != nil {
		t.Fatal(err)
	}
	check(false, false)
	check(true, false)
	r()
	check(true, true)
	// OS locks disappear on exit even when explicit cleanup does not run.
	check(false, true)
}
func TestRejectSymlinkLock(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(home, true); err == nil {
		t.Fatal("followed lock symlink")
	}
}
