package store

import "golang.org/x/sys/unix"

func publishQueue(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
