//go:build !darwin && !linux

package backup

import "errors"

func renameNoReplace(from, to string) error {
	return errors.New("restore is supported on macOS and Linux only")
}
