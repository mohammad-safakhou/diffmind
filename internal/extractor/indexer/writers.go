package indexer

import "io"

// mergeWriters returns a writer that fans out to both targets. If one
// is nil, the other is returned directly to avoid allocation overhead
// on the hot stdout/stderr path.
func mergeWriters(primary, tee io.Writer) io.Writer {
	if tee == nil {
		return primary
	}
	if primary == nil {
		return tee
	}
	return io.MultiWriter(primary, tee)
}
