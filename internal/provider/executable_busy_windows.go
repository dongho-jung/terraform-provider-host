//go:build windows

package provider

func isExecutableBusyError(error) bool {
	return false
}
