// Package backup implements versioned, byte-preserving offline workspace snapshots.
// It does not rewrite paths or migrate application schemas.
package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
)

const FormatVersion = 1
const DefaultMaxBytes int64 = 100 << 30
const maxManifestBytes = 32 << 20
const maxEntries = 200000

type Entry struct {
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`
	Mode     int64     `json:"mode"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	SHA256   string    `json:"sha256,omitempty"`
	Link     string    `json:"link,omitempty"`
}
type Manifest struct {
	Format  int       `json:"format"`
	Version string    `json:"diffmind_version"`
	Created time.Time `json:"created"`
	Home    string    `json:"source_home"`
	Bytes   int64     `json:"bytes"`
	Entries []Entry   `json:"entries"`
}
type Report struct {
	Format  int       `json:"format"`
	Version string    `json:"diffmind_version"`
	Created time.Time `json:"created"`
	Home    string    `json:"source_home"`
	Entries int       `json:"entries"`
	Bytes   int64     `json:"bytes"`
	SHA256  string    `json:"sha256"`
}

func canonicalExisting(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
func canonicalDestination(p string) (string, error) {
	if p == "" {
		return "", errors.New("destination is required")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	parent, err := canonicalExisting(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("destination parent must exist: %w", err)
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}
func absent(p string) error {
	_, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("destination already exists; nothing will be overwritten: %s", p)
}
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// Create refuses live cooperating processes and publishes only a complete archive.
// Callers must also stop old binaries and any non-cooperating writers.
func Create(home, destination, version string, maxBytes int64) (Report, error) {
	var zero Report
	home, err := canonicalExisting(home)
	if err != nil {
		return zero, err
	}
	info, err := os.Lstat(filepath.Join(home, "projects"))
	if err != nil || !info.IsDir() {
		return zero, errors.New("source is not a workspace: projects directory is required")
	}
	destination, err = canonicalDestination(destination)
	if err != nil {
		return zero, err
	}
	if within(home, destination) {
		return zero, errors.New("backup archive must be outside DIFFMIND_HOME")
	}
	if err := absent(destination); err != nil {
		return zero, err
	}
	release, err := homelock.Acquire(home, true)
	if err != nil {
		return zero, err
	}
	defer release()
	root, err := os.OpenRoot(home)
	if err != nil {
		return zero, err
	}
	defer root.Close()
	m, err := inventory(root, home, version, maxBytes)
	if err != nil {
		return zero, err
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".diffmind-backup-*")
	if err != nil {
		return zero, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, hash))
	tw := tar.NewWriter(gz)
	body, err := json.Marshal(m)
	if err != nil {
		return zero, err
	}
	if len(body) > maxManifestBytes {
		return zero, errors.New("backup manifest exceeds size limit")
	}
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		return zero, err
	}
	if _, err := tw.Write(body); err != nil {
		return zero, err
	}
	for _, e := range m.Entries {
		h := entryHeader(e)
		if err := tw.WriteHeader(h); err != nil {
			return zero, err
		}
		if e.Kind != "file" {
			continue
		}
		input, err := root.Open(filepath.FromSlash(e.Path))
		if err != nil {
			return zero, err
		}
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() {
			input.Close()
			return zero, fmt.Errorf("source changed: %s", e.Path)
		}
		digest := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(tw, digest), io.LimitReader(input, e.Size+1))
		input.Close()
		if copyErr != nil {
			return zero, copyErr
		}
		if n != e.Size || hex.EncodeToString(digest.Sum(nil)) != e.SHA256 {
			return zero, fmt.Errorf("source changed during backup: %s", e.Path)
		}
	}
	if err := tw.Close(); err != nil {
		return zero, err
	}
	if err := gz.Close(); err != nil {
		return zero, err
	}
	if err := f.Sync(); err != nil {
		return zero, err
	}
	if err := f.Close(); err != nil {
		return zero, err
	}
	// Link is an atomic no-overwrite publication on supported local filesystems.
	if err := os.Link(f.Name(), destination); err != nil {
		return zero, err
	}
	return report(m, hex.EncodeToString(hash.Sum(nil))), nil
}

func inventory(root *os.Root, home, version string, maxBytes int64) (Manifest, error) {
	m := Manifest{Format: FormatVersion, Version: version, Created: time.Now().UTC(), Home: home}
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." || p == homelock.FileName {
			return nil
		}
		if len(m.Entries) >= maxEntries {
			return errors.New("too many backup entries")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{Path: p, Mode: int64(info.Mode().Perm()), Modified: info.ModTime().UTC()}
		switch {
		case info.IsDir():
			e.Kind = "directory"
		case info.Mode().IsRegular():
			e.Kind, e.Size = "file", info.Size()
			if maxBytes <= 0 || e.Size > maxBytes-m.Bytes {
				return errors.New("backup exceeds --max-bytes")
			}
			m.Bytes += e.Size
			f, err := root.Open(filepath.FromSlash(p))
			if err != nil {
				return err
			}
			h := sha256.New()
			n, copyErr := io.Copy(h, io.LimitReader(f, e.Size+1))
			f.Close()
			if copyErr != nil {
				return copyErr
			}
			if n != e.Size {
				return fmt.Errorf("source changed: %s", p)
			}
			e.SHA256 = hex.EncodeToString(h.Sum(nil))
		case info.Mode()&os.ModeSymlink != 0:
			e.Kind = "symlink"
			e.Link, err = root.Readlink(filepath.FromSlash(p))
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported special file %s; stop services and remove sockets/FIFOs from the snapshot source", p)
		}
		m.Entries = append(m.Entries, e)
		return nil
	})
	if err != nil {
		return m, err
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	return m, validate(m, maxBytes)
}

func safePath(p string) bool {
	return p != "." && fs.ValidPath(p) && !strings.ContainsAny(p, "\\:\x00")
}
func validate(m Manifest, maxBytes int64) error {
	if m.Format != FormatVersion {
		return fmt.Errorf("unsupported backup format %d (supported: %d)", m.Format, FormatVersion)
	}
	if !filepath.IsAbs(m.Home) || filepath.Clean(m.Home) != m.Home || m.Created.IsZero() {
		return errors.New("invalid backup metadata")
	}
	if maxBytes <= 0 || m.Bytes < 0 || m.Bytes > maxBytes || len(m.Entries) > maxEntries {
		return errors.New("backup exceeds entry/byte limits")
	}
	entries := make(map[string]Entry, len(m.Entries))
	var size int64
	previous := ""
	for _, e := range m.Entries {
		if !safePath(e.Path) || e.Path <= previous || e.Path == homelock.FileName || e.Mode < 0 || e.Mode > 0o777 || e.Size < 0 {
			return fmt.Errorf("invalid or duplicate backup path/metadata: %q", e.Path)
		}
		previous = e.Path
		if parent := path.Dir(e.Path); parent != "." && entries[parent].Kind != "directory" {
			return fmt.Errorf("missing or unsafe parent: %s", e.Path)
		}
		switch e.Kind {
		case "file":
			decoded, err := hex.DecodeString(e.SHA256)
			if err != nil || len(decoded) != sha256.Size || e.Link != "" || e.Size > maxBytes-size {
				return fmt.Errorf("invalid file metadata: %s", e.Path)
			}
			size += e.Size
		case "directory", "symlink":
			if e.Size != 0 || e.SHA256 != "" || (e.Kind == "directory" && e.Link != "") {
				return fmt.Errorf("invalid entry metadata: %s", e.Path)
			}
		default:
			return fmt.Errorf("unsupported backup entry kind %q", e.Kind)
		}
		entries[e.Path] = e
	}
	if entries["projects"].Kind != "directory" || size != m.Bytes {
		return errors.New("invalid workspace or byte count")
	}
	for _, e := range m.Entries {
		if e.Kind != "symlink" {
			continue
		}
		if e.Link == "" || path.IsAbs(e.Link) || strings.ContainsAny(e.Link, "\\:\x00") {
			return fmt.Errorf("unsafe symlink: %s", e.Path)
		}
		target := path.Clean(path.Join(path.Dir(e.Path), e.Link))
		if !safePath(target) || (entries[target].Kind != "file" && entries[target].Kind != "directory") {
			return fmt.Errorf("symlink must target a saved file/directory, not another link or an external/missing path: %s", e.Path)
		}
	}
	return nil
}
func entryHeader(e Entry) *tar.Header {
	h := &tar.Header{Name: "data/" + e.Path, Mode: e.Mode, Size: e.Size, ModTime: e.Modified, Format: tar.FormatPAX}
	switch e.Kind {
	case "file":
		h.Typeflag = tar.TypeReg
	case "directory":
		h.Typeflag = tar.TypeDir
	case "symlink":
		h.Typeflag = tar.TypeSymlink
		h.Linkname = e.Link
	}
	return h
}
func report(m Manifest, digest string) Report {
	return Report{m.Format, m.Version, m.Created, m.Home, len(m.Entries), m.Bytes, digest}
}

// Verify validates the entire archive without writing workspace files. An
// optional trusted checksum detects changes to the manifest as well as contents.
func Verify(archive, expectedSHA string, maxBytes int64) (Report, error) {
	m, digest, err := consume(archive, "", expectedSHA, maxBytes)
	return report(m, digest), err
}

// Restore stages and verifies all files before a no-replace rename. Destination
// must not exist. Path mismatch is opt-in: absolute paths are never rewritten.
func Restore(archive, destination, expectedSHA string, maxBytes int64, allowPathMismatch bool) (Report, error) {
	var zero Report
	destination, err := canonicalDestination(destination)
	if err != nil {
		return zero, err
	}
	if err := absent(destination); err != nil {
		return zero, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".diffmind-restore-*")
	if err != nil {
		return zero, err
	}
	defer removeStage(stage)
	m, digest, err := consume(archive, stage, expectedSHA, maxBytes)
	if err != nil {
		return zero, err
	}
	if m.Home != destination && !allowPathMismatch {
		return zero, fmt.Errorf("backup expects home %s; use --allow-path-mismatch only for a recovery drill (stored paths are unchanged)", m.Home)
	}
	if err := renameNoReplace(stage, destination); err != nil {
		return zero, err
	}
	return report(m, digest), nil
}

// A validated archive may contain read-only directories. Make only our private
// staging directories writable before rollback; never follow archived symlinks.
func removeStage(stage string) {
	_ = filepath.WalkDir(stage, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			return os.Chmod(p, 0o700)
		}
		return err
	})
	_ = os.RemoveAll(stage)
}

func consume(archive, stage, expectedSHA string, maxBytes int64) (Manifest, string, error) {
	var m Manifest
	if expectedSHA != "" {
		b, err := hex.DecodeString(expectedSHA)
		if err != nil || len(b) != sha256.Size {
			return m, "", errors.New("--sha256 must contain exactly 64 hex digits")
		}
	}
	f, err := os.Open(archive)
	if err != nil {
		return m, "", err
	}
	defer f.Close()
	hash := sha256.New()
	br := bufio.NewReader(io.TeeReader(f, hash))
	gz, err := gzip.NewReader(br)
	if err != nil {
		return m, "", err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil {
		return m, "", err
	}
	if h.Name != "manifest.json" || h.Typeflag != tar.TypeReg || h.Size > maxManifestBytes || h.Size < 0 {
		return m, "", errors.New("missing or oversized backup manifest")
	}
	dec := json.NewDecoder(tr)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, "", err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return m, "", errors.New("trailing manifest data")
	}
	if err := validate(m, maxBytes); err != nil {
		return m, "", err
	}
	for _, e := range m.Entries {
		h, err := tr.Next()
		if err != nil {
			return m, "", err
		}
		want := entryHeader(e)
		if h.Name != want.Name || h.Typeflag != want.Typeflag || h.Size != want.Size || h.Mode != want.Mode || h.Linkname != want.Linkname || !h.ModTime.Equal(want.ModTime) {
			return m, "", fmt.Errorf("archive header disagrees with manifest: %s", e.Path)
		}
		target := filepath.Join(stage, filepath.FromSlash(e.Path))
		if e.Kind == "directory" && stage != "" {
			if err := os.Mkdir(target, 0o700); err != nil {
				return m, "", err
			}
		}
		if e.Kind != "file" {
			continue
		}
		var out *os.File
		var sink io.Writer = io.Discard
		if stage != "" {
			out, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return m, "", err
			}
			sink = out
		}
		digest := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(sink, digest), tr)
		if out != nil {
			if copyErr == nil {
				copyErr = out.Sync()
			}
			if closeErr := out.Close(); copyErr == nil {
				copyErr = closeErr
			}
		}
		if copyErr != nil {
			return m, "", copyErr
		}
		if n != e.Size || !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), e.SHA256) {
			return m, "", fmt.Errorf("checksum mismatch: %s", e.Path)
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		return m, "", errors.New("unexpected entries or damaged tar trailer")
	}
	// Consume the gzip footer to verify its CRC; allow only bounded zero padding
	// after the tar end marker and reject concatenated gzip streams/trailing data.
	padding, err := io.ReadAll(io.LimitReader(gz, (1<<20)+1))
	if err != nil {
		return m, "", err
	}
	if len(padding) > 1<<20 || strings.Trim(string(padding), "\x00") != "" {
		return m, "", errors.New("invalid archive padding")
	}
	if _, err := br.Peek(1); err != io.EOF {
		return m, "", errors.New("trailing archive data")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA != "" && !strings.EqualFold(expectedSHA, digest) {
		return m, "", errors.New("archive SHA-256 mismatch")
	}
	if stage != "" {
		for _, e := range m.Entries {
			if e.Kind == "symlink" {
				if err := os.Symlink(e.Link, filepath.Join(stage, filepath.FromSlash(e.Path))); err != nil {
					return m, "", err
				}
			}
		}
		for i := len(m.Entries) - 1; i >= 0; i-- {
			e := m.Entries[i]
			if e.Kind == "symlink" {
				continue
			}
			target := filepath.Join(stage, filepath.FromSlash(e.Path))
			if err := os.Chmod(target, os.FileMode(e.Mode)); err != nil {
				return m, "", err
			}
			if err := os.Chtimes(target, e.Modified, e.Modified); err != nil {
				return m, "", err
			}
		}
	}
	return m, digest, nil
}
