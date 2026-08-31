package indexer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResolveImage returns the image reference to use for a run. Precedence:
//  1. Explicit RunRequest.Image (when non-empty).
//  2. DIFFMIND_INDEXER_IMAGE env var.
//  3. DefaultImage.
func ResolveImage(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if envImg := os.Getenv("DIFFMIND_INDEXER_IMAGE"); envImg != "" {
		return envImg
	}
	return DefaultImage
}

// validateRequest checks paths exist and are absolute.
func validateRequest(req RunRequest) error {
	if req.SourcePath == "" {
		return errors.New("source path is required")
	}
	if req.OutputPath == "" {
		return errors.New("output path is required")
	}
	if !filepath.IsAbs(req.SourcePath) {
		return fmt.Errorf("source path must be absolute: %q", req.SourcePath)
	}
	if !filepath.IsAbs(req.OutputPath) {
		return fmt.Errorf("output path must be absolute: %q", req.OutputPath)
	}
	if st, err := os.Stat(req.SourcePath); err != nil || !st.IsDir() {
		return fmt.Errorf("source path is not a directory: %q", req.SourcePath)
	}
	if err := os.MkdirAll(req.OutputPath, 0o755); err != nil {
		return fmt.Errorf("output path: %w", err)
	}
	return nil
}
