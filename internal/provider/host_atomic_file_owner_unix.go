//go:build !windows

package provider

import (
	"fmt"
	"os"
	"syscall"
)

func preserveAtomicFileOwnership(file *os.File, destinationInfo os.FileInfo) error {
	stat, ok := destinationInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("read numeric ownership for %q", destinationInfo.Name())
	}
	return file.Chown(int(stat.Uid), int(stat.Gid))
}
