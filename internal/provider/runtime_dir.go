package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const providerRuntimeDirName = ".terraform-provider-host"

var providerRuntimeDirOverride struct {
	sync.RWMutex
	path string
}

func setProviderRuntimeDir(path string) {
	providerRuntimeDirOverride.Lock()
	defer providerRuntimeDirOverride.Unlock()
	providerRuntimeDirOverride.path = path
}

func providerRuntimeDir() (string, error) {
	providerRuntimeDirOverride.RLock()
	override := providerRuntimeDirOverride.path
	providerRuntimeDirOverride.RUnlock()
	if override != "" {
		return override, nil
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}

	return filepath.Join(workingDir, providerRuntimeDirName), nil
}

func providerRuntimeSubdir(name string) (string, error) {
	runtimeDir, err := providerRuntimeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(runtimeDir, name), nil
}
