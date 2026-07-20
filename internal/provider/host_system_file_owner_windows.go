//go:build windows

package provider

import (
	"fmt"
	"os"
)

func hostSystemFileNumericOwnership(info os.FileInfo) (string, string, error) {
	return "", "", fmt.Errorf("host system files are not supported on Windows")
}
