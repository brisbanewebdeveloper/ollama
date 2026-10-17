//go:build !windows

package server

import (
	"errors"
	"os"
	"syscall"
)

func privateDrainDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode().Perm()&0077 == 0 && stat.Uid == uint32(os.Geteuid())
}

func staleDrainSocket(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
