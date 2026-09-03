package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAgentSetupInstallsAndReturnsLaunchConfiguration(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "private work")
	bin := filepath.Join(tmp, "bin with spaces")
	var out, logs bytes.Buffer
	args := []string{"--repo-root", root, "--home", home, "--bin-dir", bin, "--name", "diffmind-test"}
	if err = run(args, &out, &logs); err != nil {
		t.Fatalf("%v %s", err, logs.String())
	}
	var result setupResult
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	entry := result.MCPServers["diffmind-test"]
	if entry.Command != filepath.Join(bin, "diffmind") || !reflect.DeepEqual(entry.Args, []string{"agent"}) || entry.Env["DIFFMIND_HOME"] != home {
		t.Fatalf("%+v", result)
	}
	if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("workspace not private", err)
	}
	if strings.Contains(out.String(), "GITHUB_TOKEN") {
		t.Fatal("credential leaked")
	}
	// A second setup never overwrites an installation without explicit authority.
	if err = run(args, &bytes.Buffer{}, &logs); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	// No client settings are mutated by the installer; registration is handled
	// by the host agent using the machine-readable result.
}
func TestSetupRejectsUnsafeTargetsBeforeBuild(t *testing.T) {
	root, _ := filepath.Abs("../..")
	for _, kind := range []string{"public-home", "symlink-home", "symlink-binary", "existing-binary", "wrong-root"} {
		t.Run(kind, func(t *testing.T) {
			tmp := t.TempDir()
			opts := setupOptions{Repo: root, Home: filepath.Join(tmp, "work"), BinDir: filepath.Join(tmp, "bin"), Name: "test"}
			if err := os.Mkdir(opts.BinDir, 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public-home":
				os.Mkdir(opts.Home, 0755)
			case "symlink-home":
				os.Symlink(tmp, opts.Home)
			case "symlink-binary":
				os.Symlink(filepath.Join(tmp, "target"), filepath.Join(opts.BinDir, "diffmind"))
				opts.Replace = true
			case "existing-binary":
				os.WriteFile(filepath.Join(opts.BinDir, "diffmind"), []byte("keep"), 0700)
			case "wrong-root":
				opts.Repo = tmp
			}
			if _, err := setup(opts, &bytes.Buffer{}); err == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
}
