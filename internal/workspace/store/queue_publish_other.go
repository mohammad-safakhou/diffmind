//go:build !darwin && !linux

package store

import "errors"

func publishQueue(from, to string) error {
	return errors.New("queue migration is supported on macOS and Linux only")
}
