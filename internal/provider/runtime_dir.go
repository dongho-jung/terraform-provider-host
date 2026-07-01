package provider

import (
	"fmt"
	"os"
	"path/filepath"
)

const providerRuntimeDirName = ".terraform-provider-host"

func providerRuntimeDir() (string, error) {
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
