package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncHostFileContentWritesWholeFileWithoutMarkers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := syncHostFileContent(path, "export EDITOR=nvim"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read content: %s", err)
	}

	if string(got) != "export EDITOR=nvim\n" {
		t.Fatalf("got %q", string(got))
	}
	if strings.Contains(string(got), "Terraform") {
		t.Fatalf("expected no Terraform markers, got:\n%s", string(got))
	}
}

func TestCleanHostFileBlocksRenderWithoutMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	runtimeDir := t.TempDir()
	options := "setopt share_history\n"
	specs := []hostFileBlockSpec{
		{Name: "options", Order: 0, Content: &options},
		{Name: "alias", Order: 1},
		{Name: "init", Order: 2},
	}
	if err := syncCleanHostFileBlocksForRuntime(path, specs, runtimeDir); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-z", nil, nil, "alias z=z", runtimeDir); err != nil {
		t.Fatalf("upsert alias z: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-a", nil, nil, "alias a=a", runtimeDir); err != nil {
		t.Fatalf("upsert alias a: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "init", "id-starship", nil, nil, `eval "$(starship init zsh)"`, runtimeDir); err != nil {
		t.Fatalf("upsert init: %s", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered file: %s", err)
	}
	got := string(data)
	want := strings.Join([]string{
		"setopt share_history\n",
		"alias a=a\nalias z=z\n",
		`eval "$(starship init zsh)"` + "\n",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "Terraform") {
		t.Fatalf("expected no Terraform markers, got:\n%s", got)
	}
}

func TestCleanHostFileManagedBlocksRespectAfterReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	runtimeDir := t.TempDir()
	if err := syncCleanHostFileBlocksForRuntime(path, testHostFileBlockSpecs("alias"), runtimeDir); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-z", nil, nil, "alias z=z", runtimeDir); err != nil {
		t.Fatalf("upsert alias z: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-a", nil, []string{"id-z"}, "alias a=a", runtimeDir); err != nil {
		t.Fatalf("upsert alias a: %s", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered file: %s", err)
	}
	got := string(data)
	if got != "alias z=z\nalias a=a\n" {
		t.Fatalf("got:\n%s", got)
	}
	if strings.Contains(got, "Terraform") {
		t.Fatalf("expected no Terraform markers, got:\n%s", got)
	}
}

func TestCleanHostFileManagedBlockUpdateAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	runtimeDir := t.TempDir()
	if err := syncCleanHostFileBlocksForRuntime(path, testHostFileBlockSpecs("alias"), runtimeDir); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-foo", nil, nil, "alias foo=foo", runtimeDir); err != nil {
		t.Fatalf("upsert foo: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-bar", nil, nil, "alias bar=bar", runtimeDir); err != nil {
		t.Fatalf("upsert bar: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-foo", nil, nil, "alias foo=foobar", runtimeDir); err != nil {
		t.Fatalf("update foo: %s", err)
	}
	if err := removeCleanHostFileManagedBlockForRuntime(path, "alias", "id-bar", runtimeDir); err != nil {
		t.Fatalf("remove bar: %s", err)
	}

	block, ok, err := readCleanManagedBlockForRuntime(path, "alias", "id-foo", runtimeDir)
	if err != nil {
		t.Fatalf("read foo: %s", err)
	}
	if !ok {
		t.Fatal("expected foo to exist")
	}
	if block.body != "alias foo=foobar\n" {
		t.Fatalf("got body %q", block.body)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered file: %s", err)
	}
	got := string(data)
	if got != "alias foo=foobar\n" {
		t.Fatalf("got:\n%s", got)
	}
	if strings.Contains(got, "alias bar=bar") {
		t.Fatalf("expected bar to be removed:\n%s", got)
	}
}

func TestPlannedCleanHostFileContentIgnoresRenderedFileDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	runtimeDir := t.TempDir()
	if err := syncCleanHostFileBlocksForRuntime(path, testHostFileBlockSpecs("alias"), runtimeDir); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrderForRuntime(path, "alias", "id-foo", nil, nil, "alias foo=foo", runtimeDir); err != nil {
		t.Fatalf("upsert foo: %s", err)
	}
	if err := os.WriteFile(path, []byte("alias drift=drift\n"), 0o644); err != nil {
		t.Fatalf("write drift: %s", err)
	}

	got, err := plannedCleanHostFileContentForProvider(path, testHostFileBlockSpecs("alias"), t.TempDir(), runtimeDir)
	if err != nil {
		t.Fatalf("planned content: %s", err)
	}
	if got != "alias foo=foo\n" {
		t.Fatalf("got %q", got)
	}
}

func testHostFileBlockSpecs(names ...string) []hostFileBlockSpec {
	specs := make([]hostFileBlockSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, hostFileBlockSpec{Name: name})
	}

	return specs
}
