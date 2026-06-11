package snapshot

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type mirrorStats struct {
	dirsCreated       int
	filesCopied       int
	bytesCopied       int64
	skippedExt        int
	skippedSize       int
	skippedDir        int
	skippedNonRegular int
}

func mirrorTree(src, dst string) error {
	stats := &mirrorStats{}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if _, skip := defaultSkipDirs[entry.Name()]; skip {
				stats.skippedDir++
				util.Trace("snapshot", "skipping dir", map[string]any{"path": relative})
				return fs.SkipDir
			}
		}
		target := filepath.Join(dst, relative)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return err
			}
			stats.dirsCreated++
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(linkTarget, target)
		case mode.IsRegular():
			if _, skip := skippedExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; skip {
				stats.skippedExt++
				return nil
			}
			if info.Size() > defaultMaxFileBytes {
				stats.skippedSize++
				util.Trace("snapshot", "skipping large file", map[string]any{
					"path": relative, "bytes": info.Size(),
				})
				return nil
			}
			if err := copyRegular(path, target, mode.Perm()); err != nil {
				return err
			}
			stats.filesCopied++
			stats.bytesCopied += info.Size()
			return nil
		default:
			stats.skippedNonRegular++
			util.Trace("snapshot", "skipping non-regular file", map[string]any{
				"path": relative, "mode": mode.String(),
			})
			return nil
		}
	})
	if err == nil {
		util.Info("snapshot", "snapshot stats", map[string]any{
			"src": src, "dst": dst, "dirs": stats.dirsCreated,
			"files": stats.filesCopied, "bytes": stats.bytesCopied,
			"skipped_dirs": stats.skippedDir, "skipped_extensions": stats.skippedExt,
			"skipped_oversize":   stats.skippedSize,
			"skipped_nonregular": stats.skippedNonRegular,
			"max_file_bytes":     defaultMaxFileBytes,
		})
	}
	return err
}

func copyRegular(src, dst string, permission fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer input.Close()
	output, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, permission|0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}
