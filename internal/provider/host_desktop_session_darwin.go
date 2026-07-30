//go:build darwin

package provider

import (
	"fmt"
	"os"
	osuser "os/user"
	"strconv"
	"strings"
	"syscall"
)

func activeHostDesktopUsername() (string, error) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return "", fmt.Errorf("resolve active macOS desktop user: stat /dev/console: %w", err)
	}
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("resolve active macOS desktop user: unsupported /dev/console metadata")
	}
	account, err := osuser.LookupId(strconv.FormatUint(uint64(status.Uid), 10))
	if err != nil {
		return "", fmt.Errorf("resolve active macOS desktop user for uid %d: %w", status.Uid, err)
	}

	username := strings.TrimSpace(account.Username)
	if username == "" {
		return "", fmt.Errorf("resolve active macOS desktop user: username is empty")
	}
	return username, nil
}
