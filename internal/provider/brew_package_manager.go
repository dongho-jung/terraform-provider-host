package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	brewPackageTypeFormula = "formula"
	brewPackageTypeCask    = "cask"
)

type BrewPackageManager interface {
	TapInstalled(ctx context.Context, name string) (bool, error)
	Tap(ctx context.Context, name string) error
	PackageStatus(ctx context.Context, name string, packageType string) (BrewPackageStatus, error)
	InstallPackage(ctx context.Context, name string, packageType string) error
	UpgradePackage(ctx context.Context, name string, packageType string) error
	MarkPackageOnRequest(ctx context.Context, name string) error
	RemovePackage(ctx context.Context, name string, packageType string, autoremove bool, zap bool) error
}

type BrewPackageStatus struct {
	Name               string
	PackageType        string
	Installed          bool
	InstalledVersion   string
	CandidateVersion   string
	UpgradeVersion     string
	InstalledOnRequest bool
	Pinned             bool
	AutoUpdates        bool
}

type CLIBrewPackageManager struct {
	brewPath string
	sudoPath string
}

type commandTTY struct {
	file *os.File
}

type brewSudoLeaseState struct {
	mu     sync.Mutex
	active bool
}

var brewSudoLease brewSudoLeaseState

type brewInfoOutput struct {
	Formulae []brewFormulaInfo `json:"formulae"`
	Casks    []brewCaskInfo    `json:"casks"`
}

type brewFormulaInfo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	Installed []brewFormulaInstall `json:"installed"`
	LinkedKeg string               `json:"linked_keg"`
	Outdated  bool                 `json:"outdated"`
	Pinned    bool                 `json:"pinned"`
}

type brewFormulaInstall struct {
	Version            string `json:"version"`
	InstalledOnRequest bool   `json:"installed_on_request"`
}

type brewCaskInfo struct {
	Token       string  `json:"token"`
	FullToken   string  `json:"full_token"`
	Version     string  `json:"version"`
	Installed   *string `json:"installed"`
	Outdated    bool    `json:"outdated"`
	Pinned      bool    `json:"pinned"`
	AutoUpdates bool    `json:"auto_updates"`
}

func NewCLIBrewPackageManager(brewPath string) *CLIBrewPackageManager {
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		sudoPath = ""
	}

	return &CLIBrewPackageManager{
		brewPath: brewPath,
		sudoPath: sudoPath,
	}
}

func (m *CLIBrewPackageManager) TapInstalled(ctx context.Context, name string) (bool, error) {
	out, err := m.run(ctx, "tap")
	if err != nil {
		return false, err
	}

	for _, tap := range strings.Fields(string(out)) {
		if tap == name {
			return true, nil
		}
	}

	return false, nil
}

func (m *CLIBrewPackageManager) Tap(ctx context.Context, name string) error {
	return m.withMutationLock(ctx, func() error {
		_, err := m.run(ctx, "tap", name)
		return err
	})
}

func (m *CLIBrewPackageManager) PackageStatus(ctx context.Context, name string, packageType string) (BrewPackageStatus, error) {
	args := []string{"info", "--json=v2", brewPackageTypeFlag(packageType), name}
	out, err := m.run(ctx, args...)
	if err != nil {
		return BrewPackageStatus{}, err
	}

	return parseBrewPackageStatus(name, packageType, out)
}

func (m *CLIBrewPackageManager) InstallPackage(ctx context.Context, name string, packageType string) error {
	args := []string{"install", "--yes", brewPackageTypeFlag(packageType), name}
	if err := m.prepareMutatingPackageCommand(ctx, packageType, args); err != nil {
		return err
	}

	return m.withMutationLock(ctx, func() error {
		return m.runMutatingPackageCommand(ctx, packageType, args)
	})
}

func (m *CLIBrewPackageManager) UpgradePackage(ctx context.Context, name string, packageType string) error {
	args := []string{"upgrade", "--yes", brewPackageTypeFlag(packageType)}
	if packageType == brewPackageTypeCask {
		args = append(args, "--greedy")
	}
	args = append(args, name)

	if err := m.prepareMutatingPackageCommand(ctx, packageType, args); err != nil {
		return err
	}

	return m.withMutationLock(ctx, func() error {
		return m.runMutatingPackageCommand(ctx, packageType, args)
	})
}

func (m *CLIBrewPackageManager) MarkPackageOnRequest(ctx context.Context, name string) error {
	args := []string{"install", "--yes", "--formula", name}
	return m.withMutationLock(ctx, func() error {
		_, err := m.runWithEnv(ctx, []string{"HOMEBREW_NO_INSTALL_UPGRADE=1"}, args...)
		return err
	})
}

func (m *CLIBrewPackageManager) RemovePackage(ctx context.Context, name string, packageType string, autoremove bool, zap bool) error {
	args := []string{"uninstall", brewPackageTypeFlag(packageType)}
	if packageType == brewPackageTypeCask && zap {
		args = append(args, "--zap")
	}
	args = append(args, name)

	if err := m.prepareMutatingPackageCommand(ctx, packageType, args); err != nil {
		return err
	}

	return m.withMutationLock(ctx, func() error {
		if err := m.runMutatingPackageCommand(ctx, packageType, args); err != nil {
			return err
		}

		if packageType == brewPackageTypeFormula && autoremove {
			_, err := m.run(ctx, "autoremove")
			return err
		}

		return nil
	})
}

func (m *CLIBrewPackageManager) prepareMutatingPackageCommand(ctx context.Context, packageType string, args []string) error {
	if packageType != brewPackageTypeCask {
		return nil
	}

	return m.ensureCaskSudoLease(ctx, args...)
}

func (m *CLIBrewPackageManager) runMutatingPackageCommand(ctx context.Context, packageType string, args []string) error {
	if packageType == brewPackageTypeCask {
		return m.runInteractive(ctx, nil, args...)
	}

	_, err := m.run(ctx, args...)
	return err
}

func (m *CLIBrewPackageManager) run(ctx context.Context, args ...string) ([]byte, error) {
	return m.runWithEnv(ctx, nil, args...)
}

func (m *CLIBrewPackageManager) runWithEnv(ctx context.Context, extraEnv []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, m.brewPath, args...)
	cmd.Env = append(cmd.Environ(), extraEnv...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", m.brewPath, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func (m *CLIBrewPackageManager) runInteractive(ctx context.Context, extraEnv []string, args ...string) error {
	tty, err := openCommandTTY()
	if err != nil {
		return err
	}
	defer tty.close()

	cmd := exec.CommandContext(ctx, m.brewPath, args...)
	cmd.Env = append(cmd.Environ(), extraEnv...)
	cmd.Stdin = tty.file
	cmd.Stdout = tty.file
	cmd.Stderr = tty.file

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w", m.brewPath, strings.Join(args, " "), err)
	}

	return nil
}

func (m *CLIBrewPackageManager) withMutationLock(ctx context.Context, fn func() error) error {
	lock, err := lockHostFile("brew:" + m.brewPath)
	if err != nil {
		return err
	}
	defer lock.close()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return fn()
}

func (m *CLIBrewPackageManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0 && m.sudoPath != ""
}

func (m *CLIBrewPackageManager) keepSudoAlive(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.validateSudo(ctx)
		}
	}
}

func (m *CLIBrewPackageManager) ensureCaskSudoLease(ctx context.Context, args ...string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if m.sudoPath == "" {
		return fmt.Errorf("Homebrew cask command may require sudo, but sudo was not found in PATH")
	}

	brewSudoLease.mu.Lock()
	defer brewSudoLease.mu.Unlock()

	if err := m.validateSudo(ctx); err == nil {
		m.startSudoKeepAliveLocked()
		return nil
	}

	if err := m.authenticateSudoForCask(ctx, args...); err != nil {
		return err
	}

	m.startSudoKeepAliveLocked()
	return nil
}

func (m *CLIBrewPackageManager) startSudoKeepAliveLocked() {
	if brewSudoLease.active {
		return
	}

	brewSudoLease.active = true
	go m.keepSudoAlive(context.Background())
}

func (m *CLIBrewPackageManager) validateSudo(ctx context.Context) error {
	tty, err := openCommandTTY()
	if err != nil {
		return err
	}
	defer tty.close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, m.sudoPath, "-n", "-v")
	cmd.Stdin = tty.file
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	return cmd.Run()
}

func (m *CLIBrewPackageManager) authenticateSudoForCask(ctx context.Context, args ...string) error {
	tty, err := openCommandTTY()
	if err != nil {
		return err
	}
	defer tty.close()

	fmt.Fprintln(tty.file, "")
	fmt.Fprintln(tty.file, "============================================================")
	fmt.Fprintln(tty.file, "Terraform provider host needs your macOS password")
	fmt.Fprintf(tty.file, "Homebrew cask command: %s %s\n", m.brewPath, strings.Join(args, " "))
	fmt.Fprintln(tty.file, "Terraform is paused until this password is entered.")
	fmt.Fprintln(tty.file, "If Terraform status lines bury the prompt, type your password here and press Enter.")
	fmt.Fprintln(tty.file, "You can also run `sudo -v` before `terraform apply`.")
	fmt.Fprintln(tty.file, "============================================================")

	done := make(chan struct{})
	var reminder sync.WaitGroup
	reminder.Add(1)
	go m.remindSudoPrompt(ctx, tty.file, done, &reminder)
	defer func() {
		close(done)
		reminder.Wait()
	}()

	cmd := exec.CommandContext(ctx, m.sudoPath, "-p", "\nTerraform provider host sudo password (input hidden): ", "-v")
	cmd.Stdin = tty.file
	cmd.Stdout = tty.file
	cmd.Stderr = tty.file

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo authentication failed: %w. Run `sudo -v` before `terraform apply`, or configure passwordless sudo for local package management", err)
	}

	return nil
}

func (m *CLIBrewPackageManager) remindSudoPrompt(ctx context.Context, tty *os.File, done <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(7 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			fmt.Fprintln(tty)
			fmt.Fprintln(tty, "Terraform provider host is still waiting for your sudo password.")
			fmt.Fprintln(tty, "Type it in this terminal and press Enter, or press Ctrl-C to cancel.")
		}
	}
}

func openCommandTTY() (*commandTTY, error) {
	file, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/tty for interactive Homebrew command: %w. Run Terraform from an interactive terminal, run `sudo -v` before `terraform apply`, or configure passwordless sudo for local package management", err)
	}

	return &commandTTY{file: file}, nil
}

func (t *commandTTY) close() {
	_ = t.file.Close()
}

func parseBrewPackageStatus(name string, packageType string, data []byte) (BrewPackageStatus, error) {
	var info brewInfoOutput
	if err := json.Unmarshal(data, &info); err != nil {
		return BrewPackageStatus{}, fmt.Errorf("parse brew JSON: %w", err)
	}

	switch packageType {
	case brewPackageTypeFormula:
		formula, ok := selectBrewFormula(name, info.Formulae)
		if !ok {
			return BrewPackageStatus{}, fmt.Errorf("brew formula %q was not found in brew info output", name)
		}
		return brewFormulaStatus(formula), nil
	case brewPackageTypeCask:
		cask, ok := selectBrewCask(name, info.Casks)
		if !ok {
			return BrewPackageStatus{}, fmt.Errorf("brew cask %q was not found in brew info output", name)
		}
		return brewCaskStatus(cask), nil
	default:
		return BrewPackageStatus{}, fmt.Errorf("unsupported Homebrew package type %q", packageType)
	}
}

func selectBrewFormula(name string, formulae []brewFormulaInfo) (brewFormulaInfo, bool) {
	for _, formula := range formulae {
		if formula.Name == name || formula.FullName == name {
			return formula, true
		}
	}
	if len(formulae) == 1 {
		return formulae[0], true
	}

	return brewFormulaInfo{}, false
}

func selectBrewCask(name string, casks []brewCaskInfo) (brewCaskInfo, bool) {
	for _, cask := range casks {
		if cask.Token == name || cask.FullToken == name {
			return cask, true
		}
	}
	if len(casks) == 1 {
		return casks[0], true
	}

	return brewCaskInfo{}, false
}

func brewFormulaStatus(formula brewFormulaInfo) BrewPackageStatus {
	status := BrewPackageStatus{
		Name:             formula.Name,
		PackageType:      brewPackageTypeFormula,
		CandidateVersion: formula.Versions.Stable,
		Pinned:           formula.Pinned,
	}

	if len(formula.Installed) > 0 {
		status.Installed = true
		status.InstalledVersion = formula.LinkedKeg
		if status.InstalledVersion == "" {
			status.InstalledVersion = formula.Installed[len(formula.Installed)-1].Version
		}
		for _, installed := range formula.Installed {
			if installed.InstalledOnRequest {
				status.InstalledOnRequest = true
				break
			}
		}
	}

	if status.Installed && formula.Outdated {
		status.UpgradeVersion = status.CandidateVersion
	}

	return status
}

func brewCaskStatus(cask brewCaskInfo) BrewPackageStatus {
	status := BrewPackageStatus{
		Name:               cask.Token,
		PackageType:        brewPackageTypeCask,
		CandidateVersion:   cask.Version,
		Pinned:             cask.Pinned,
		AutoUpdates:        cask.AutoUpdates,
		InstalledOnRequest: true,
	}

	if cask.Installed != nil && *cask.Installed != "" {
		status.Installed = true
		status.InstalledVersion = *cask.Installed
	}

	if status.Installed && cask.Outdated {
		status.UpgradeVersion = status.CandidateVersion
	}

	return status
}

func brewPackageTypeFlag(packageType string) string {
	switch packageType {
	case brewPackageTypeCask:
		return "--cask"
	default:
		return "--formula"
	}
}
