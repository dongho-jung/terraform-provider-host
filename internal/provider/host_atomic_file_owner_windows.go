//go:build windows

package provider

import "os"

func preserveAtomicFileOwnership(_ *os.File, _ os.FileInfo) error {
	return nil
}
