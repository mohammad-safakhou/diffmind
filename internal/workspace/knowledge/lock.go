package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const LockVersion = 1

type LockFile struct {
	Version int          `json:"version" yaml:"version"`
	Packs   []LockedPack `json:"packs" yaml:"packs"`
}

type LockedPack struct {
	ID       string `json:"id" yaml:"id"`
	Version  string `json:"version" yaml:"version"`
	Source   string `json:"source" yaml:"source"`
	Revision string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Digest   string `json:"digest" yaml:"digest"`
	Path     string `json:"path" yaml:"path"`
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Priority int    `json:"priority,omitempty" yaml:"priority,omitempty"`
}

type InstallOptions struct {
	Home   string
	Source string
	Ref    string
}

func LockPath(home string) string { return filepath.Join(home, "diffmind-packs.lock") }

func ReadLock(home string) (*LockFile, error) {
	body, err := os.ReadFile(LockPath(home))
	if os.IsNotExist(err) {
		return &LockFile{Version: LockVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pack lock: %w", err)
	}
	var lock LockFile
	dec := yaml.NewDecoder(strings.NewReader(string(body)))
	dec.KnownFields(true)
	if err := dec.Decode(&lock); err != nil {
		return nil, fmt.Errorf("parse pack lock: %w", err)
	}
	if lock.Version != LockVersion {
		return nil, fmt.Errorf("unsupported pack lock version %d", lock.Version)
	}
	sortLocked(lock.Packs)
	return &lock, nil
}

func WriteLock(home string, lock *LockFile) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	lock.Version = LockVersion
	sortLocked(lock.Packs)
	body, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(home, ".diffmind-packs.lock-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, LockPath(home))
}

func Install(options InstallOptions) (LockedPack, error) {
	if options.Home == "" || options.Source == "" {
		return LockedPack{}, errors.New("install requires home and source")
	}
	if err := os.MkdirAll(options.Home, 0o755); err != nil {
		return LockedPack{}, err
	}
	source := options.Source
	revision := ""
	cleanup := func() {}
	if info, err := os.Stat(source); err == nil {
		if !info.IsDir() {
			return LockedPack{}, fmt.Errorf("pack source %s is not a directory", source)
		}
		source, err = filepath.Abs(source)
		if err != nil {
			return LockedPack{}, err
		}
	} else if !os.IsNotExist(err) {
		return LockedPack{}, err
	} else {
		temp, err := os.MkdirTemp(options.Home, ".pack-clone-*")
		if err != nil {
			return LockedPack{}, err
		}
		cleanup = func() { _ = os.RemoveAll(temp) }
		args := []string{"clone", "--quiet", "--filter=blob:none"}
		args = append(args, options.Source, temp)
		command := exec.Command("git", args...)
		if output, err := command.CombinedOutput(); err != nil {
			cleanup()
			return LockedPack{}, fmt.Errorf("clone pack: %w: %s", err, strings.TrimSpace(string(output)))
		}
		source = temp
		if options.Ref != "" {
			command = exec.Command("git", "-C", temp, "checkout", "--quiet", "--detach", options.Ref)
			if output, err := command.CombinedOutput(); err != nil {
				cleanup()
				return LockedPack{}, fmt.Errorf("checkout pack ref %s: %w: %s", options.Ref, err, strings.TrimSpace(string(output)))
			}
		}
		command = exec.Command("git", "-C", temp, "rev-parse", "HEAD")
		output, err := command.Output()
		if err != nil {
			cleanup()
			return LockedPack{}, fmt.Errorf("resolve pack revision: %w", err)
		}
		revision = strings.TrimSpace(string(output))
	}
	defer cleanup()

	manifest, err := findSingleManifest(source)
	if err != nil {
		return LockedPack{}, err
	}
	pack, err := LoadPack(manifest)
	if err != nil {
		return LockedPack{}, err
	}
	packRoot := filepath.Dir(manifest)
	digest, err := ContentDigest(packRoot)
	if err != nil {
		return LockedPack{}, err
	}
	destination := filepath.Join(options.Home, "packs", pack.ID, pack.Version)
	if !safeInstallDestination(options.Home, destination) {
		return LockedPack{}, fmt.Errorf("unsafe pack destination %s", destination)
	}
	staging, err := os.MkdirTemp(filepath.Dir(filepath.Dir(destination)), ".install-*")
	if err != nil {
		if err := os.MkdirAll(filepath.Dir(filepath.Dir(destination)), 0o755); err != nil {
			return LockedPack{}, err
		}
		staging, err = os.MkdirTemp(filepath.Dir(filepath.Dir(destination)), ".install-*")
	}
	if err != nil {
		return LockedPack{}, err
	}
	defer os.RemoveAll(staging)
	if err := copyTree(packRoot, staging); err != nil {
		return LockedPack{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return LockedPack{}, err
	}
	if err := os.RemoveAll(destination); err != nil {
		return LockedPack{}, err
	}
	if err := os.Rename(staging, destination); err != nil {
		return LockedPack{}, err
	}

	lock, err := ReadLock(options.Home)
	if err != nil {
		return LockedPack{}, err
	}
	locked := LockedPack{
		ID: pack.ID, Version: pack.Version, Source: options.Source,
		Revision: revision, Digest: digest, Path: destination,
		Enabled: true, Priority: pack.Priority,
	}
	replaced := false
	for i := range lock.Packs {
		if lock.Packs[i].ID == pack.ID {
			locked.Enabled = lock.Packs[i].Enabled
			lock.Packs[i] = locked
			replaced = true
			break
		}
	}
	if !replaced {
		lock.Packs = append(lock.Packs, locked)
	}
	if err := WriteLock(options.Home, lock); err != nil {
		return LockedPack{}, err
	}
	return locked, nil
}

func SetEnabled(home, id string, enabled bool) error {
	lock, err := ReadLock(home)
	if err != nil {
		return err
	}
	for i := range lock.Packs {
		if lock.Packs[i].ID == id {
			lock.Packs[i].Enabled = enabled
			return WriteLock(home, lock)
		}
	}
	return fmt.Errorf("knowledge pack %q is not installed", id)
}

// LoadEnabled verifies every enabled pack against its locked content digest.
func LoadEnabled(home string) ([]*Pack, error) {
	lock, err := ReadLock(home)
	if err != nil {
		return nil, err
	}
	var packs []*Pack
	var errs []error
	for _, entry := range lock.Packs {
		if !entry.Enabled {
			continue
		}
		digest, err := ContentDigest(entry.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.ID, err))
			continue
		}
		if digest != entry.Digest {
			errs = append(errs, fmt.Errorf("%s: locked digest %s does not match installed content %s", entry.ID, entry.Digest, digest))
			continue
		}
		manifest, err := findSingleManifest(entry.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.ID, err))
			continue
		}
		pack, err := LoadPack(manifest)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		packs = append(packs, pack)
	}
	if setErrs := ValidateSet(packs); len(setErrs) > 0 {
		errs = append(errs, errors.New(FormatValidationErrors(setErrs)))
	}
	sort.Slice(packs, func(i, j int) bool {
		if packs[i].Priority != packs[j].Priority {
			return packs[i].Priority > packs[j].Priority
		}
		return packs[i].ID < packs[j].ID
	})
	return packs, errors.Join(errs...)
}

func LockDigest(lock *LockFile) (string, error) {
	copy := *lock
	copy.Packs = append([]LockedPack(nil), lock.Packs...)
	sortLocked(copy.Packs)
	body, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ActiveSetDigest fingerprints the exact content of every pack used by an
// analysis, including project-local packs that are not in the global lock.
func ActiveSetDigest(packs []*Pack) (string, error) {
	type item struct {
		ID, Version, Digest string
	}
	items := make([]item, 0, len(packs))
	for _, pack := range packs {
		var digest string
		if pack.SourcePath == "" {
			body, err := json.Marshal(pack)
			if err != nil {
				return "", err
			}
			sum := sha256.Sum256(body)
			digest = "sha256:" + hex.EncodeToString(sum[:])
		} else {
			var err error
			digest, err = ContentDigest(filepath.Dir(pack.SourcePath))
			if err != nil {
				return "", fmt.Errorf("%s: %w", pack.ID, err)
			}
		}
		items = append(items, item{ID: pack.ID, Version: pack.Version, Digest: digest})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return items[i].Version < items[j].Version
	})
	body, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ContentDigest(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in knowledge packs: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, rel := range paths {
		if _, err := io.WriteString(hash, rel+"\x00"); err != nil {
			return "", err
		}
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func findSingleManifest(root string) (string, error) {
	var manifests []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(entry.Name())
		if name == "pack.yaml" || name == "pack.yml" || name == "pack.json" {
			manifests = append(manifests, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(manifests) != 1 {
		return "", fmt.Errorf("expected exactly one pack manifest, found %d", len(manifests))
	}
	return manifests[0], nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in knowledge packs: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func safeInstallDestination(home, destination string) bool {
	rel, err := filepath.Rel(filepath.Join(home, "packs"), destination)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sortLocked(packs []LockedPack) {
	sort.Slice(packs, func(i, j int) bool {
		if packs[i].ID != packs[j].ID {
			return packs[i].ID < packs[j].ID
		}
		return packs[i].Version < packs[j].Version
	})
}
