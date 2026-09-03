// release-check validates the actual native release archive through the public
// installer, isolated CLI/server operations, and real-company acceptance suite.
package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

func main() {
	archive := flag.String("archive", "", "native release archive")
	version := flag.String("version", "", "expected release version")
	repo := flag.String("repo-root", ".", "source checkout for installer and acceptance fixtures")
	packageBinary := flag.String("package-binary", "", "optionally create the archive from this native binary first (no overwrite)")
	flag.Parse()
	if *packageBinary != "" {
		if !versionPattern.MatchString(*version) {
			fmt.Fprintln(os.Stderr, "explicit semantic version required")
			os.Exit(1)
		}
		if err := packageArchive(*archive, *packageBinary, *repo); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := run(*archive, *version, *repo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Generate exactly three plain entries, without machine ownership, macOS
// resource forks/xattrs, local usernames or filesystem creation timestamps.
// Binary build metadata supplies release provenance; Git history is untouched.
func packageArchive(destination, binary, repo string) error {
	if destination == "" {
		return errors.New("archive destination required")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("archive destination already exists or is unavailable")
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".release-package-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, file := range []struct {
		name, path string
		mode       int64
	}{{"diffmind", binary, 0755}, {"LICENSE", filepath.Join(repo, "LICENSE"), 0644}, {"README.md", filepath.Join(repo, "README.md"), 0644}} {
		info, err := os.Lstat(file.path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 512<<20 {
			return errors.New("package input must be a nonempty bounded regular file")
		}
		if file.name == "diffmind" && info.Mode().Perm()&0111 == 0 {
			return errors.New("package binary must be executable")
		}
		input, err := os.Open(file.path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: info.Size(), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}); err != nil {
			input.Close()
			return err
		}
		n, err := io.Copy(tw, io.LimitReader(input, info.Size()+1))
		input.Close()
		if err != nil {
			return err
		}
		if n != info.Size() {
			return errors.New("package input changed")
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), destination)
}

func checkArchive(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buffer := bufio.NewReader(f)
	gz, err := gzip.NewReader(buffer)
	if err != nil {
		return err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	want := map[string]bool{"diffmind": false, "LICENSE": false, "README.md": false}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		seen, ok := want[h.Name]
		if !ok || seen || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > 512<<20 {
			return fmt.Errorf("release archive must contain only one regular diffmind, LICENSE and README.md: rejected %q type=%d size=%d duplicate=%t", h.Name, h.Typeflag, h.Size, seen)
		}
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || len(h.PAXRecords) != 0 || len(h.Xattrs) != 0 || h.ModTime.Unix() != 0 {
			return errors.New("release archive contains non-neutral host metadata")
		}
		if h.Name == "diffmind" && h.Mode&0111 == 0 {
			return errors.New("release binary is not executable")
		}
		want[h.Name] = true
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return err
		}
	}
	for name, found := range want {
		if !found {
			return fmt.Errorf("missing release entry %s", name)
		}
	}
	padding, err := io.ReadAll(io.LimitReader(gz, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(padding) > 1<<20 || strings.Trim(string(padding), "\x00") != "" {
		return errors.New("invalid release archive padding")
	}
	if _, err := buffer.Peek(1); err != io.EOF {
		return errors.New("trailing release archive data")
	}
	return nil
}

func isolatedEnv(extra ...string) []string {
	var result []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DIFFMIND_") && !strings.HasPrefix(entry, "GITHUB_TOKEN=") && !strings.HasPrefix(entry, "GH_TOKEN=") {
			result = append(result, entry)
		}
	}
	return append(result, extra...)
}

func command(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return out, nil
}

func run(archive, version, repo string) error {
	if !versionPattern.MatchString(version) || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		return errors.New("a supported native platform and explicit semantic version are required")
	}
	name := "diffmind_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if filepath.Base(archive) != name {
		return fmt.Errorf("expected native archive %s", name)
	}
	if err := checkArchive(archive); err != nil {
		return err
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "diffmind-release-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	releases := filepath.Join(temp, "releases")
	destination := filepath.Join(releases, "download", "v"+version)
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	source, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer source.Close()
	copy, err := os.OpenFile(filepath.Join(destination, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(copy, hash), source)
	closeErr := copy.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.WriteFile(filepath.Join(destination, "checksums.txt"), []byte(hex.EncodeToString(hash.Sum(nil))+"  "+name+"\n"), 0600); err != nil {
		return err
	}
	home := filepath.Join(temp, "workspace")
	bin := filepath.Join(temp, "bin")
	baseURL := (&url.URL{Scheme: "file", Path: releases}).String()
	env := isolatedEnv("DIFFMIND_HOME="+home, "DIFFMIND_INSTALL_DIR="+bin, "DIFFMIND_VERSION="+version, "DIFFMIND_RELEASE_BASE_URL="+baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := command(ctx, temp, env, "sh", filepath.Join(repo, "install.sh")); err != nil {
		return err
	}
	binary := filepath.Join(bin, "diffmind")
	data, err := command(ctx, temp, env, binary, "version", "--json")
	if err != nil {
		return err
	}
	var build struct{ Version, OS, Arch string }
	if err := json.Unmarshal(data, &build); err != nil {
		return err
	}
	if build.Version != version || build.OS != runtime.GOOS || build.Arch != runtime.GOARCH {
		return fmt.Errorf("installed build mismatch: %s", data)
	}
	for _, args := range [][]string{{"doctor", "--json"}, {"list", "projects"}, {"storage", "migrate", "--offline", "--json"}, {"storage", "verify", "--offline", "--json"}} {
		if _, err := command(ctx, temp, env, binary, args...); err != nil {
			return err
		}
	}
	if err := checkDashboard(ctx, temp, env, binary); err != nil {
		return err
	}
	// Reuse the exact Go/Python/Java graph, MCP, incremental/retry and managed
	// backup recovery tests with the installed artifact, not a rebuilt analyzer.
	acceptanceEnv := isolatedEnv("DIFFMIND_ACCEPTANCE_BINARY=" + binary)
	if _, err := command(ctx, repo, acceptanceEnv, "go", "test", "./internal/workspace/ui", "-run", "^TestCompanyAcceptance(SQLite)?$", "-count=1", "-timeout=5m"); err != nil {
		return err
	}
	fmt.Printf("Native release verified: %s %s/%s; installer, embedded UI, SQLite, graph/MCP and managed recovery passed\n", version, runtime.GOOS, runtime.GOARCH)
	return nil
}

func checkDashboard(ctx context.Context, dir string, env []string, binary string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cmd := exec.CommandContext(ctx, binary, "ui", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-spa-rebuild")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		response, err := client.Get(base + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("installed dashboard did not start")
		case <-tick.C:
		}
	}
	response, err := client.Get(base + "/")
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if err != nil {
		return err
	}
	if response.StatusCode != 200 {
		return errors.New("embedded index unavailable")
	}
	asset := regexp.MustCompile(`src="(/assets/[^"\s]+\.js)"`).FindSubmatch(body)
	if len(asset) != 2 {
		return errors.New("embedded index has no JavaScript asset")
	}
	response, err = client.Get(base + string(asset[1]))
	if err != nil {
		return err
	}
	body, err = io.ReadAll(io.LimitReader(response.Body, 2<<20))
	response.Body.Close()
	if err != nil {
		return err
	}
	if response.StatusCode != 200 || len(body) < 1000 {
		return errors.New("embedded JavaScript asset unavailable")
	}
	return nil
}
