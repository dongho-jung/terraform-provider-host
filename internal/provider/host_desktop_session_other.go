//go:build !darwin

package provider

import "fmt"

func activeHostDesktopUsername() (string, error) {
	return "", fmt.Errorf("active desktop session detection is supported only on macOS")
}
