package backup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
)

const catalogFile = "diffmind-backups.json"
const snapshotPrefix = "snapshot-"

type backupCatalog struct {
	Format int    `json:"format"`
	ID     string `json:"id"`
	Home   string `json:"source_home"`
}

type snapshotReceipt struct {
	Format     int    `json:"format"`
	CatalogID  string `json:"catalog_id"`
	SnapshotID string `json:"snapshot_id"`
	Report     Report `json:"archive"`
}

type ManagedSnapshot struct {
	ID      string `json:"id"`
	Archive string `json:"archive"`
	Report  Report `json:"report"`
}

type RotationReport struct {
	Created  ManagedSnapshot   `json:"created"`
	Retained []ManagedSnapshot `json:"retained"`
	Removed  []string          `json:"removed"`
}

func randomID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func validSnapshotID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && strings.ToLower(id) == id
}

func privateJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func readPrivateJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return errors.New("unsafe or oversized backup metadata")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("backup metadata changed while opening")
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing backup metadata")
	}
	return nil
}

// openCatalog never adopts a nonempty directory or another workspace's catalog.
// Its private local directory and nonblocking lock exclude cooperating rotators.
// Callers must keep unrelated/untrusted writers out of both source and catalog.
func openCatalog(home, directory string, initialize bool) (backupCatalog, string, func(), error) {
	var catalog backupCatalog
	originalHome := home
	home, err := canonicalExisting(home)
	if err != nil && !initialize && errors.Is(err, os.ErrNotExist) {
		home, err = canonicalDestination(originalHome)
	}
	if err != nil {
		return catalog, "", nil, err
	}
	if initialize {
		info, err := os.Lstat(filepath.Join(home, "projects"))
		if err != nil || !info.IsDir() {
			return catalog, "", nil, errors.New("source is not a workspace")
		}
	}
	directory, err = canonicalDestination(directory)
	if err != nil {
		return catalog, "", nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return catalog, "", nil, errors.New("backup directory must exist, be a real directory, and be private (0700)")
	}
	if within(home, directory) || within(directory, home) {
		return catalog, "", nil, errors.New("backup catalog and workspace must not contain each other")
	}
	release, err := homelock.Acquire(directory, true)
	if err != nil {
		return catalog, "", nil, fmt.Errorf("backup catalog busy or unavailable: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	err = readPrivateJSON(filepath.Join(directory, catalogFile), &catalog)
	if errors.Is(err, os.ErrNotExist) && initialize {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return catalog, "", nil, err
		}
		for _, entry := range entries {
			if entry.Name() != homelock.FileName {
				return catalog, "", nil, errors.New("refusing to initialize a nonempty backup directory")
			}
		}
		id, err := randomID()
		if err != nil {
			return catalog, "", nil, err
		}
		catalog = backupCatalog{Format: 1, ID: id, Home: home}
		// A crash before publication leaves a staging marker; it is never adopted
		// or removed automatically. Inspection is required before retrying.
		staging := filepath.Join(directory, ".catalog-"+id)
		defer os.Remove(staging)
		if err := privateJSON(staging, catalog); err != nil {
			return catalog, "", nil, err
		}
		if err := renameNoReplace(staging, filepath.Join(directory, catalogFile)); err != nil {
			return catalog, "", nil, err
		}
		if err := syncDirectory(directory); err != nil {
			return catalog, "", nil, err
		}
	} else if err != nil {
		return catalog, "", nil, err
	}
	if catalog.Format != 1 || !validSnapshotID(catalog.ID) || catalog.Home != home {
		return catalog, "", nil, errors.New("unsupported or mismatched backup catalog")
	}
	ok = true
	return catalog, directory, release, nil
}

func catalogSnapshots(catalog backupCatalog, directory string, maxBytes int64) ([]ManagedSnapshot, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	if len(entries) > 10002 {
		return nil, errors.New("backup catalog entry limit exceeded")
	}
	result := []ManagedSnapshot{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), snapshotPrefix) {
			continue
		}
		id := strings.TrimPrefix(entry.Name(), snapshotPrefix)
		if !validSnapshotID(id) || !entry.IsDir() {
			return nil, fmt.Errorf("invalid managed snapshot %q", entry.Name())
		}
		folder := filepath.Join(directory, entry.Name())
		children, err := os.ReadDir(folder)
		if err != nil {
			return nil, err
		}
		if len(children) != 2 {
			return nil, fmt.Errorf("unexpected files in managed snapshot %s", id)
		}
		for _, child := range children {
			info, err := child.Info()
			if err != nil {
				return nil, err
			}
			if (child.Name() != "archive.tar.gz" && child.Name() != "receipt.json") || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return nil, fmt.Errorf("unsafe file in managed snapshot %s", id)
			}
		}
		var receipt snapshotReceipt
		if err := readPrivateJSON(filepath.Join(folder, "receipt.json"), &receipt); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", id, err)
		}
		if receipt.Format != 1 || receipt.CatalogID != catalog.ID || receipt.SnapshotID != id || receipt.Report.Home != catalog.Home || len(receipt.Report.SHA256) != 64 {
			return nil, fmt.Errorf("snapshot %s receipt mismatch", id)
		}
		archive := filepath.Join(folder, "archive.tar.gz")
		verified, err := Verify(archive, receipt.Report.SHA256, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s verification failed: %w", id, err)
		}
		if verified != receipt.Report {
			return nil, fmt.Errorf("snapshot %s metadata mismatch", id)
		}
		result = append(result, ManagedSnapshot{ID: id, Archive: archive, Report: verified})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Report.Created.Equal(result[j].Report.Created) {
			return result[i].ID > result[j].ID
		}
		return result[i].Report.Created.After(result[j].Report.Created)
	})
	return result, nil
}

// ManagedList fully verifies catalog-owned archives; unrelated files are ignored.
// Listing never locks source writers and works when the original home is missing.
func ManagedList(home, directory string, maxBytes int64) ([]ManagedSnapshot, error) {
	if maxBytes <= 0 {
		return nil, errors.New("max bytes must be positive")
	}
	catalog, directory, release, err := openCatalog(home, directory, false)
	if err != nil {
		return nil, err
	}
	defer release()
	return catalogSnapshots(catalog, directory, maxBytes)
}

// Rotate creates and independently verifies a new snapshot BEFORE applying
// keep-last retention to this catalog's verified snapshots only. It never
// prunes live workspace records. Failure before publication deletes no backups.
// After publication an error may leave extra snapshots or a .retired-* folder;
// these are reported, never silently treated as a successful retention cycle.
func Rotate(home, directory, version string, keep int, maxBytes int64) (RotationReport, error) {
	result := RotationReport{Retained: []ManagedSnapshot{}, Removed: []string{}}
	if keep < 1 || keep > 1000 || maxBytes <= 0 {
		return result, errors.New("keep-last must be 1..1000 and max bytes positive")
	}
	catalog, directory, release, err := openCatalog(home, directory, true)
	if err != nil {
		return result, err
	}
	defer release()
	previous, err := catalogSnapshots(catalog, directory, maxBytes)
	if err != nil {
		return result, err
	}
	id, err := randomID()
	if err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp(directory, ".pending-")
	if err != nil {
		return result, err
	}
	// Only the two files this invocation creates are cleaned up. Never recurse
	// into an arbitrary caller-supplied directory, even on a failed operation.
	defer func() {
		_ = os.Remove(filepath.Join(stage, "archive.tar.gz"))
		_ = os.Remove(filepath.Join(stage, "receipt.json"))
		_ = os.Remove(stage)
	}()
	archive := filepath.Join(stage, "archive.tar.gz")
	created, err := Create(catalog.Home, archive, version, maxBytes)
	if err != nil {
		return result, err
	}
	verified, err := Verify(archive, created.SHA256, maxBytes)
	if err != nil {
		return result, err
	}
	if verified != created {
		return result, errors.New("new backup verification mismatch")
	}
	if err := privateJSON(filepath.Join(stage, "receipt.json"), snapshotReceipt{Format: 1, CatalogID: catalog.ID, SnapshotID: id, Report: created}); err != nil {
		return result, err
	}
	if err := syncDirectory(stage); err != nil {
		return result, err
	}
	final := filepath.Join(directory, snapshotPrefix+id)
	if err := renameNoReplace(stage, final); err != nil {
		return result, err
	}
	result.Created = ManagedSnapshot{ID: id, Archive: filepath.Join(final, "archive.tar.gz"), Report: created}
	result.Retained = append(result.Retained, result.Created)
	if err := syncDirectory(directory); err != nil {
		return result, err
	}
	// The newly verified snapshot is always retained, even after a clock rollback.
	for i, old := range previous {
		if i < keep-1 {
			result.Retained = append(result.Retained, old)
			continue
		}
		folder := filepath.Dir(old.Archive)
		retired := filepath.Join(directory, ".retired-"+old.ID)
		if err := renameNoReplace(folder, retired); err != nil {
			return result, fmt.Errorf("new snapshot saved; retention failed: %w", err)
		}
		if err := syncDirectory(directory); err != nil {
			return result, err
		}
		// Exact known filenames only. Any unexpected children prevent removal.
		for _, name := range []string{"archive.tar.gz", "receipt.json"} {
			if err := os.Remove(filepath.Join(retired, name)); err != nil {
				return result, fmt.Errorf("new snapshot saved; retention cleanup failed: %w", err)
			}
		}
		if err := os.Remove(retired); err != nil {
			return result, err
		}
		result.Removed = append(result.Removed, old.ID)
	}
	return result, syncDirectory(directory)
}
