package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileHostFileBlocksAddsNamedBlocks(t *testing.T) {
	t.Parallel()

	got, err := reconcileHostFileBlocks("export PATH\n", testHostFileBlockSpecs("functions", "alias"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	for _, marker := range []string{
		fileBlockBeginMarker("alias"),
		fileBlockEndMarker("alias"),
		fileBlockBeginMarker("functions"),
		fileBlockEndMarker("functions"),
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected output to contain %q:\n%s", marker, got)
		}
	}

	if strings.Index(got, fileBlockBeginMarker("alias")) > strings.Index(got, fileBlockBeginMarker("functions")) {
		t.Fatalf("expected blocks to be sorted by name:\n%s", got)
	}
}

func TestEnsureHostFileBlockDoesNotRemoveSiblingBlocks(t *testing.T) {
	t.Parallel()

	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecs("alias", "functions"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	got, err := ensureHostFileBlock(content, "alias")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !strings.Contains(got, fileBlockBeginMarker("functions")) {
		t.Fatalf("expected sibling block to be preserved:\n%s", got)
	}
}

func TestUpsertManagedBlockPreservesSiblings(t *testing.T) {
	t.Parallel()

	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecs("alias"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-foo", "alias foo=foobar")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-bar", "alias bar=barbaz")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-foo", "alias foo='foo bar'")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	for _, want := range []string{"alias foo='foo bar'", "alias bar=barbaz"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected content to contain %q:\n%s", want, content)
		}
	}

	if strings.Contains(content, "alias foo=foobar") {
		t.Fatalf("expected old foo alias to be replaced:\n%s", content)
	}
}

func TestRemoveManagedBlockRemovesOnlyTargetBlock(t *testing.T) {
	t.Parallel()

	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecs("alias"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-foo", "alias foo=foobar")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-bar", "alias bar=barbaz")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	got, err := removeManagedBlock(content, "alias", "id-foo")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if strings.Contains(got, "alias foo=foobar") {
		t.Fatalf("expected foo alias to be removed:\n%s", got)
	}
	if !strings.Contains(got, "alias bar=barbaz") {
		t.Fatalf("expected bar alias to be preserved:\n%s", got)
	}
}

func TestExtractManagedBlockBodyPreservesConfiguredTrailingNewline(t *testing.T) {
	t.Parallel()

	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecs("functions"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "functions", "id-foo", "foo() { echo foo }\n")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	body, _, ok, err := extractManagedBlockBody(content, "functions", "id-foo")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !ok {
		t.Fatal("expected managed block to exist")
	}
	if body != "foo() { echo foo }\n" {
		t.Fatalf("got body %q", body)
	}
}

func TestUpsertManagedBlockSortsByContent(t *testing.T) {
	t.Parallel()

	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecs("alias"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-z", "alias z=z")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "alias", "id-a", "alias a=a")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	a := strings.Index(content, "alias a=a")
	z := strings.Index(content, "alias z=z")

	if !(a < z) {
		t.Fatalf("expected content order:\n%s", content)
	}
}

func TestUpsertManagedBlockSortsByAfterReferences(t *testing.T) {
	t.Parallel()

	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecs("alias"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlockWithOrder(content, "alias", "id-z", nil, nil, "alias z=z")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlockWithOrder(content, "alias", "id-a", nil, []string{"id-z"}, "alias a=a")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	z := strings.Index(content, "alias z=z")
	a := strings.Index(content, "alias a=a")
	if !(z < a) {
		t.Fatalf("expected after reference to override lexical content order:\n%s", content)
	}
	if !strings.Contains(content, managedBlockAfterMarker([]string{"id-z"})) {
		t.Fatalf("expected after marker to be persisted:\n%s", content)
	}
}

func TestParseManagedBlockLinesSupportsLegacyBlocks(t *testing.T) {
	t.Parallel()

	block, err := parseManagedBlockLines(splitHostFileLines("# BEGIN Terraform host_file_block id-legacy\nalias legacy=legacy\n# END Terraform host_file_block id-legacy\n"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if block.body != "alias legacy=legacy\n" {
		t.Fatalf("got body %q", block.body)
	}
}

func TestParseManagedBlockLinesSkipsLegacyPriorityMarker(t *testing.T) {
	t.Parallel()

	block, err := parseManagedBlockLines(splitHostFileLines("# BEGIN Terraform host_file_block id-legacy\n# Terraform host_file_block priority 10\nalias legacy=legacy\n# END Terraform host_file_block id-legacy\n"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if block.body != "alias legacy=legacy\n" {
		t.Fatalf("got body %q", block.body)
	}
}

func TestReconcileHostFileBlocksSetsInlineContentAndPreservesManagedBlocks(t *testing.T) {
	t.Parallel()

	initial := testHostFileBlockSpecsWithContent("options", "setopt autocd\n")
	content, err := reconcileHostFileBlocks("", initial)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	content, err = upsertManagedBlock(content, "options", "id-starship", `eval "$(starship init zsh)"`)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	updated := testHostFileBlockSpecsWithContent("options", "setopt autocd\nsetopt hist_ignore_all_dups\n")
	got, err := reconcileHostFileBlocks(content, updated)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if strings.Contains(got, "setopt autocd\n# BEGIN Terraform host_file_block") {
		t.Fatalf("expected inline content to include new line before managed blocks:\n%s", got)
	}
	for _, want := range []string{
		"setopt autocd\nsetopt hist_ignore_all_dups\n",
		`eval "$(starship init zsh)"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q:\n%s", want, got)
		}
	}

	if strings.Index(got, "setopt hist_ignore_all_dups") > strings.Index(got, `eval "$(starship init zsh)"`) {
		t.Fatalf("expected inline content before managed blocks:\n%s", got)
	}
}

func TestExtractHostFileBlockInlineContentIgnoresManagedBlocks(t *testing.T) {
	t.Parallel()

	lines := splitHostFileLines(strings.Join([]string{
		"setopt autocd\n",
		renderManagedBlock("id-starship", `eval "$(starship init zsh)"`),
		"setopt share_history\n",
	}, ""))

	got, err := extractHostFileBlockInlineContent(lines)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := "setopt autocd\nsetopt share_history\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadHostFileBlockSpecsRefreshesInlineContent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".zshrc")
	content, err := reconcileHostFileBlocks("", testHostFileBlockSpecsWithContent("options", "setopt autocd\n"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	content, err = upsertManagedBlock(content, "options", "id-starship", `eval "$(starship init zsh)"`)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	content = strings.Replace(content, "setopt autocd\n", "setopt share_history\n", 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %s", err)
	}

	specs, exists, err := readHostFileBlockSpecs(path, testHostFileBlockSpecsWithContent("options", "setopt autocd\n"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !exists {
		t.Fatal("expected host file block to exist")
	}
	if specs[0].Content == nil || *specs[0].Content != "setopt share_history" {
		t.Fatalf("expected refreshed inline content, got %#v", specs[0].Content)
	}
}

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
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(t.TempDir(), ".zshrc")
	options := "setopt share_history\n"
	specs := []hostFileBlockSpec{
		{Name: "options", Order: 0, Content: &options},
		{Name: "alias", Order: 1},
		{Name: "init", Order: 2},
	}
	if err := syncCleanHostFileBlocks(path, specs); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "alias", "id-z", "alias z=z"); err != nil {
		t.Fatalf("upsert alias z: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "alias", "id-a", "alias a=a"); err != nil {
		t.Fatalf("upsert alias a: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "init", "id-starship", `eval "$(starship init zsh)"`); err != nil {
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
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := syncCleanHostFileBlocks(path, testHostFileBlockSpecs("alias")); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrder(path, "alias", "id-z", nil, nil, "alias z=z"); err != nil {
		t.Fatalf("upsert alias z: %s", err)
	}
	if err := upsertCleanHostFileManagedBlockWithOrder(path, "alias", "id-a", nil, []string{"id-z"}, "alias a=a"); err != nil {
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
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := syncCleanHostFileBlocks(path, testHostFileBlockSpecs("alias")); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "alias", "id-foo", "alias foo=foo"); err != nil {
		t.Fatalf("upsert foo: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "alias", "id-bar", "alias bar=bar"); err != nil {
		t.Fatalf("upsert bar: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "alias", "id-foo", "alias foo=foobar"); err != nil {
		t.Fatalf("update foo: %s", err)
	}
	if err := removeCleanHostFileManagedBlock(path, "alias", "id-bar"); err != nil {
		t.Fatalf("remove bar: %s", err)
	}

	body, _, ok, err := readCleanManagedBlockBody(path, "alias", "id-foo")
	if err != nil {
		t.Fatalf("read foo: %s", err)
	}
	if !ok {
		t.Fatal("expected foo to exist")
	}
	if body != "alias foo=foobar\n" {
		t.Fatalf("got body %q", body)
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
	t.Setenv("HOME", t.TempDir())

	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := syncCleanHostFileBlocks(path, testHostFileBlockSpecs("alias")); err != nil {
		t.Fatalf("sync clean blocks: %s", err)
	}
	if err := upsertCleanHostFileManagedBlock(path, "alias", "id-foo", "alias foo=foo"); err != nil {
		t.Fatalf("upsert foo: %s", err)
	}
	if err := os.WriteFile(path, []byte("alias drift=drift\n"), 0o644); err != nil {
		t.Fatalf("write drift: %s", err)
	}

	got, err := plannedCleanHostFileContent(path, testHostFileBlockSpecs("alias"))
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

func testHostFileBlockSpecsWithContent(name string, content string) []hostFileBlockSpec {
	return []hostFileBlockSpec{
		{
			Name:    name,
			Content: &content,
		},
	}
}
