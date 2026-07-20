//go:build !windows

package provider

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func hostSystemFileNumericOwnership(info os.FileInfo) (string, string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", fmt.Errorf("read numeric ownership for %q", info.Name())
	}
	return strconv.FormatUint(uint64(stat.Uid), 10), strconv.FormatUint(uint64(stat.Gid), 10), nil
}
