//go:build !darwin && !linux

package homelock

import (
	"errors"
	"os"
)

func acquire(path string, exclusive bool) (*os.File, error) {
	if exclusive {
		return nil, errors.New("offline maintenance is supported on macOS and Linux only")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
