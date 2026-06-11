package snapshot

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func forceRemove(path string) error {
	if path == "" {
		return nil
	}
	if err := os.RemoveAll(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrPermission) {
		if chmodErr := chmodWalk(path); chmodErr != nil {
			return fmt.Errorf("snapshot: chmod walk failed: %w (original: %v)", chmodErr, err)
		}
		return os.RemoveAll(path)
	} else {
		if chmodErr := chmodWalk(path); chmodErr != nil {
			return fmt.Errorf("snapshot: chmod walk failed: %w (original: %v)", chmodErr, err)
		}
		return os.RemoveAll(path)
	}
}

func chmodWalk(root string) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Chmod(path, 0o700)
		return nil
	})
}
