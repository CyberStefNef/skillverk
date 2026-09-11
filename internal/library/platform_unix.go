//go:build !windows

package library

import (
	"golang.org/x/sys/unix"
	"os"
)

func fileLock(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX); e != nil {
		f.Close()
		return nil, e
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
func directoryLink(target, path string) error { return os.Symlink(target, path) }
func replaceFile(from, to string) error       { return os.Rename(from, to) }
