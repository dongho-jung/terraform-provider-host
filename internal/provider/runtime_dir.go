package provider

import (
	"fmt"
	"os"
	"path/filepath"
)

const providerRuntimeDirName = ".terraform-provider-host"

func providerRuntimeDir() (string, error) {
	return providerRuntimeDirForRuntime("")
}

func providerRuntimeDirForRuntime(runtimeDir string) (string, error) {
	if runtimeDir != "" {
		return runtimeDir, nil
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}

	return filepath.Join(workingDir, providerRuntimeDirName), nil
}

func providerRuntimeSubdirForRuntime(runtimeDir string, name string) (string, error) {
	resolvedRuntimeDir, err := providerRuntimeDirForRuntime(runtimeDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedRuntimeDir, name), nil
}
