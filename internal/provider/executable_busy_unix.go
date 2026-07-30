//go:build !windows

package provider

import (
	"errors"
	"syscall"
)

func isExecutableBusyError(err error) bool {
	return errors.Is(err, syscall.ETXTBSY)
}
