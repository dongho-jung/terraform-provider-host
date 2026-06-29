package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	hostFileBlockBeginPrefix        = "# BEGIN Terraform host_file block "
	hostFileBlockEndPrefix          = "# END Terraform host_file block "
	hostFileManagedBlockBeginPrefix = "# BEGIN Terraform host_file_block "
	hostFileManagedBlockEndPrefix   = "# END Terraform host_file_block "
	hostFileManagedBlockPriority    = "# Terraform host_file_block priority "
)

type hostFileManagedBlock struct {
	id       string
	priority int64
	body     string
}

type cleanHostFileState struct {
	Path   string                             `json:"path"`
	Blocks map[string]cleanHostFileBlockState `json:"blocks"`
}

type cleanHostFileBlockState struct {
	Priority int64                                     `json:"priority,omitempty"`
	Content  string                                    `json:"content,omitempty"`
	Managed  map[string]cleanHostFileManagedBlockState `json:"managed,omitempty"`
}

type cleanHostFileManagedBlockState struct {
	Priority int64  `json:"priority,omitempty"`
	Content  string `json:"content"`
}

type lockedHostFile struct {
	lockFile *os.File
}

func withLockedHostFile(ctx context.Context, path string, fn func(path string) error) error {
	resolvedPath, err := expandHostPath(path)
	if err != nil {
		return err
	}

	lock, err := lockHostFile(resolvedPath)
	if err != nil {
		return err
	}
	defer lock.close()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return fn(resolvedPath)
}

func lockHostFile(path string) (*lockedHostFile, error) {
	sum := sha256.Sum256([]byte(path))
	lockPath := filepath.Join(os.TempDir(), "terraform-provider-host-"+hex.EncodeToString(sum[:])+".lock")

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", lockPath, err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock file %q: %w", lockPath, err)
	}

	return &lockedHostFile{lockFile: lockFile}, nil
}

func (f *lockedHostFile) close() {
	_ = syscall.Flock(int(f.lockFile.Fd()), syscall.LOCK_UN)
	_ = f.lockFile.Close()
}

func syncHostFileBlocks(path string, specs []hostFileBlockSpec) error {
	if err := validateHostFileBlockSpecs(specs); err != nil {
		return err
	}

	content, err := readHostFile(path)
	if err != nil {
		return err
	}

	next, err := reconcileHostFileBlocks(content, specs)
	if err != nil {
		return err
	}

	return writeHostFile(path, next)
}

func deleteHostFileBlocks(path string, names []string) error {
	if err := validateHostFileBlockNames(names); err != nil {
		return err
	}

	content, err := readHostFileIfExists(path)
	if err != nil {
		return err
	}
	if content == "" {
		return nil
	}

	next, err := removeHostFileBlocks(content, names)
	if err != nil {
		return err
	}

	return writeHostFile(path, next)
}

func upsertHostFileManagedBlock(path string, fileBlockName string, blockID string, priority int64, content string) error {
	if err := validateHostFileBlockName(fileBlockName); err != nil {
		return err
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return err
	}

	fileContent, err := readHostFile(path)
	if err != nil {
		return err
	}

	withFileBlock, err := ensureHostFileBlock(fileContent, fileBlockName)
	if err != nil {
		return err
	}

	next, err := upsertManagedBlock(withFileBlock, fileBlockName, blockID, priority, content)
	if err != nil {
		return err
	}

	return writeHostFile(path, next)
}

func removeHostFileManagedBlock(path string, fileBlockName string, blockID string) error {
	if err := validateHostFileBlockName(fileBlockName); err != nil {
		return err
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return err
	}

	fileContent, err := readHostFileIfExists(path)
	if err != nil {
		return err
	}
	if fileContent == "" {
		return nil
	}

	next, err := removeManagedBlock(fileContent, fileBlockName, blockID)
	if err != nil {
		return err
	}

	return writeHostFile(path, next)
}

func readManagedBlockBody(path string, fileBlockName string, blockID string) (string, int64, bool, error) {
	fileContent, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("read %q: %w", path, err)
	}

	return extractManagedBlockBody(string(fileContent), fileBlockName, blockID)
}

func readHostFileBlockSpecs(path string, specs []hostFileBlockSpec) ([]hostFileBlockSpec, bool, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %q: %w", path, err)
	}

	lines := splitHostFileLines(string(content))
	next := append([]hostFileBlockSpec(nil), specs...)
	for i, spec := range next {
		if err := validateHostFileBlockName(spec.Name); err != nil {
			return nil, false, err
		}

		fileStart, fileEnd, ok, err := findFileBlockRange(lines, spec.Name)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		if spec.Content == nil {
			continue
		}

		inlineContent, err := extractHostFileBlockInlineContent(lines[fileStart+1 : fileEnd])
		if err != nil {
			return nil, false, err
		}
		if inlineContent != canonicalHostFileInlineContent(*spec.Content) {
			content := trimRenderedManagedBlockBody(inlineContent)
			if *spec.Content == "" && inlineContent != "" {
				content = inlineContent
			}
			next[i].Content = &content
		}
	}

	return next, true, nil
}

func hostFileHasBlocks(path string, names []string) (bool, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %q: %w", path, err)
	}

	for _, name := range names {
		if err := validateHostFileBlockName(name); err != nil {
			return false, err
		}
		if _, _, ok, err := findFileBlockRange(splitHostFileLines(string(content)), name); err != nil {
			return false, err
		} else if !ok {
			return false, nil
		}
	}

	return true, nil
}

func readHostFile(path string) (string, error) {
	content, err := readHostFileIfExists(path)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create parent directory for %q: %w", path, err)
	}

	return content, nil
}

func readHostFileIfExists(path string) (string, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}

	return string(content), nil
}

func writeHostFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}

	return nil
}

func syncHostFileContent(path string, content string) error {
	if _, err := readHostFile(path); err != nil {
		return err
	}

	return writeHostFile(path, canonicalHostFileContent(content))
}

func readHostFileContent(path string) (string, bool, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %q: %w", path, err)
	}

	return string(content), true, nil
}

func deleteHostFile(path string) error {
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("remove %q: %w", path, err)
	}

	return nil
}

func syncCleanHostFileBlocks(path string, specs []hostFileBlockSpec) error {
	if err := validateHostFileBlockSpecs(specs); err != nil {
		return err
	}

	state, err := readCleanHostFileState(path)
	if err != nil {
		return err
	}
	state.Path = path
	if state.Blocks == nil {
		state.Blocks = map[string]cleanHostFileBlockState{}
	}

	desired := make(map[string]hostFileBlockSpec, len(specs))
	for _, spec := range specs {
		desired[spec.Name] = spec
	}
	for name := range state.Blocks {
		if _, ok := desired[name]; !ok {
			delete(state.Blocks, name)
		}
	}
	for _, spec := range specs {
		block := state.Blocks[spec.Name]
		block.Priority = spec.Priority
		if spec.Content != nil {
			block.Content = canonicalHostFileInlineContent(*spec.Content)
		} else {
			block.Content = ""
		}
		state.Blocks[spec.Name] = block
	}

	return writeCleanHostFileStateAndContent(path, state)
}

func deleteCleanHostFile(path string) error {
	if err := deleteHostFile(path); err != nil {
		return err
	}

	statePath, err := cleanHostFileStatePath(path)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("remove clean host file state %q: %w", statePath, err)
	}

	return nil
}

func upsertCleanHostFileManagedBlock(path string, fileBlockName string, blockID string, priority int64, content string) error {
	if err := validateHostFileBlockName(fileBlockName); err != nil {
		return err
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return err
	}

	state, err := readCleanHostFileState(path)
	if err != nil {
		return err
	}
	state.Path = path
	if state.Blocks == nil {
		state.Blocks = map[string]cleanHostFileBlockState{}
	}

	block := state.Blocks[fileBlockName]
	if block.Managed == nil {
		block.Managed = map[string]cleanHostFileManagedBlockState{}
	}
	block.Managed[blockID] = cleanHostFileManagedBlockState{
		Priority: priority,
		Content:  canonicalManagedBlockBody(content),
	}
	state.Blocks[fileBlockName] = block

	return writeCleanHostFileStateAndContent(path, state)
}

func removeCleanHostFileManagedBlock(path string, fileBlockName string, blockID string) error {
	if err := validateHostFileBlockName(fileBlockName); err != nil {
		return err
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return err
	}

	state, exists, err := readCleanHostFileStateIfExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	block := state.Blocks[fileBlockName]
	delete(block.Managed, blockID)
	if len(block.Managed) == 0 {
		block.Managed = nil
	}
	state.Blocks[fileBlockName] = block

	return writeCleanHostFileStateAndContent(path, state)
}

func readCleanManagedBlockBody(path string, fileBlockName string, blockID string) (string, int64, bool, error) {
	state, exists, err := readCleanHostFileStateIfExists(path)
	if err != nil || !exists {
		return "", 0, false, err
	}

	block, ok := state.Blocks[fileBlockName]
	if !ok {
		return "", 0, false, nil
	}
	managed, ok := block.Managed[blockID]
	if !ok {
		return "", 0, false, nil
	}

	return managed.Content, managed.Priority, true, nil
}

func readCleanHostFileBlockSpecs(path string, specs []hostFileBlockSpec) ([]hostFileBlockSpec, bool, error) {
	state, exists, err := readCleanHostFileStateIfExists(path)
	if err != nil || !exists {
		return nil, false, err
	}

	next := append([]hostFileBlockSpec(nil), specs...)
	for i, spec := range next {
		block, ok := state.Blocks[spec.Name]
		if !ok {
			return nil, false, nil
		}
		next[i].Priority = block.Priority
		if spec.Content != nil {
			content := trimRenderedManagedBlockBody(block.Content)
			if *spec.Content == "" && block.Content != "" {
				content = block.Content
			}
			next[i].Content = &content
		}
	}

	return next, true, nil
}

func readCleanHostFileState(path string) (cleanHostFileState, error) {
	state, _, err := readCleanHostFileStateIfExists(path)
	if err != nil {
		return cleanHostFileState{}, err
	}
	if state.Blocks == nil {
		state.Blocks = map[string]cleanHostFileBlockState{}
	}

	return state, nil
}

func readCleanHostFileStateIfExists(path string) (cleanHostFileState, bool, error) {
	statePath, err := cleanHostFileStatePath(path)
	if err != nil {
		return cleanHostFileState{}, false, err
	}

	content, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return cleanHostFileState{Path: path, Blocks: map[string]cleanHostFileBlockState{}}, false, nil
	}
	if err != nil {
		return cleanHostFileState{}, false, fmt.Errorf("read clean host file state %q: %w", statePath, err)
	}

	var state cleanHostFileState
	if err := json.Unmarshal(content, &state); err != nil {
		return cleanHostFileState{}, false, fmt.Errorf("parse clean host file state %q: %w", statePath, err)
	}
	if state.Blocks == nil {
		state.Blocks = map[string]cleanHostFileBlockState{}
	}

	return state, true, nil
}

func writeCleanHostFileStateAndContent(path string, state cleanHostFileState) error {
	if err := writeHostFile(path, renderCleanHostFileState(state)); err != nil {
		return err
	}

	statePath, err := cleanHostFileStatePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return fmt.Errorf("create clean host file state directory for %q: %w", statePath, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode clean host file state %q: %w", statePath, err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return fmt.Errorf("write clean host file state %q: %w", statePath, err)
	}

	return nil
}

func renderCleanHostFileState(state cleanHostFileState) string {
	blocks := make([]struct {
		name  string
		block cleanHostFileBlockState
	}, 0, len(state.Blocks))
	for name, block := range state.Blocks {
		blocks = append(blocks, struct {
			name  string
			block cleanHostFileBlockState
		}{name: name, block: block})
	}
	sort.SliceStable(blocks, func(i int, j int) bool {
		if blocks[i].block.Priority != blocks[j].block.Priority {
			return blocks[i].block.Priority < blocks[j].block.Priority
		}

		return blocks[i].name < blocks[j].name
	})

	sections := []string{}
	for _, item := range blocks {
		content := renderCleanHostFileBlockState(item.block)
		if strings.TrimSpace(content) == "" {
			continue
		}
		sections = append(sections, content)
	}
	if len(sections) == 0 {
		return ""
	}

	return strings.Join(sections, "\n")
}

func renderCleanHostFileBlockState(block cleanHostFileBlockState) string {
	var builder strings.Builder
	if block.Content != "" {
		builder.WriteString(canonicalHostFileInlineContent(block.Content))
	}

	managed := make([]hostFileManagedBlock, 0, len(block.Managed))
	for id, item := range block.Managed {
		managed = append(managed, hostFileManagedBlock{
			id:       id,
			priority: item.Priority,
			body:     item.Content,
		})
	}
	sortHostFileManagedBlocks(managed)

	for _, item := range managed {
		builder.WriteString(canonicalManagedBlockBody(item.body))
	}

	return builder.String()
}

func cleanHostFileStatePath(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	sum := sha256.Sum256([]byte(path))

	return filepath.Join(home, ".terraform-provider-host", "host_files", hex.EncodeToString(sum[:])+".json"), nil
}

func reconcileHostFileBlocks(content string, specs []hostFileBlockSpec) (string, error) {
	desired := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		desired[spec.Name] = struct{}{}
	}

	next, err := filterHostFileBlocks(content, desired)
	if err != nil {
		return "", err
	}

	for _, spec := range sortedHostFileBlockSpecs(specs) {
		lines := splitHostFileLines(next)
		_, _, ok, err := findFileBlockRange(lines, spec.Name)
		if err != nil {
			return "", err
		}
		if !ok {
			next = appendHostFileBlock(next, spec.Name)
		}
	}

	for _, spec := range sortedHostFileBlockSpecs(specs) {
		if spec.Content == nil {
			continue
		}

		next, err = setHostFileBlockContent(next, spec.Name, *spec.Content)
		if err != nil {
			return "", err
		}
	}

	return next, nil
}

func ensureHostFileBlock(content string, name string) (string, error) {
	lines := splitHostFileLines(content)
	_, _, ok, err := findFileBlockRange(lines, name)
	if err != nil {
		return "", err
	}
	if ok {
		return content, nil
	}

	return appendHostFileBlock(content, name), nil
}

func filterHostFileBlocks(content string, desired map[string]struct{}) (string, error) {
	lines := splitHostFileLines(content)
	if len(lines) == 0 {
		return "", nil
	}

	next := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		name, ok := parseFileBlockBegin(lineBody(lines[i]))
		if !ok {
			next = append(next, lines[i])
			i++
			continue
		}

		end := findMarkerLine(lines, i+1, fileBlockEndMarker(name))
		if end == -1 {
			return "", fmt.Errorf("managed file block %q is missing its end marker", name)
		}

		if _, keep := desired[name]; keep {
			next = append(next, lines[i:end+1]...)
		}
		i = end + 1
	}

	return strings.Join(next, ""), nil
}

func removeHostFileBlocks(content string, names []string) (string, error) {
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		remove[name] = struct{}{}
	}

	lines := splitHostFileLines(content)
	if len(lines) == 0 {
		return "", nil
	}

	next := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		name, ok := parseFileBlockBegin(lineBody(lines[i]))
		if !ok {
			next = append(next, lines[i])
			i++
			continue
		}

		end := findMarkerLine(lines, i+1, fileBlockEndMarker(name))
		if end == -1 {
			return "", fmt.Errorf("managed file block %q is missing its end marker", name)
		}

		if _, drop := remove[name]; !drop {
			next = append(next, lines[i:end+1]...)
		}
		i = end + 1
	}

	return strings.Join(next, ""), nil
}

func appendHostFileBlock(content string, name string) string {
	var builder strings.Builder
	builder.WriteString(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		builder.WriteString("\n")
	}
	if content != "" && !strings.HasSuffix(content, "\n\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(fileBlockBeginMarker(name))
	builder.WriteString("\n")
	builder.WriteString(fileBlockEndMarker(name))
	builder.WriteString("\n")

	return builder.String()
}

func setHostFileBlockContent(content string, name string, blockContent string) (string, error) {
	lines := splitHostFileLines(content)
	fileStart, fileEnd, ok, err := findFileBlockRange(lines, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("managed file block %q was not found", name)
	}

	managed, err := sortedManagedBlockLines(lines[fileStart+1 : fileEnd])
	if err != nil {
		return "", err
	}

	nextInner := hostFileInlineContentLines(blockContent)
	nextInner = append(nextInner, managed...)
	lines = replaceLines(lines, fileStart+1, fileEnd, nextInner)

	return strings.Join(lines, ""), nil
}

func hostFileInlineContentLines(content string) []string {
	if content == "" {
		return nil
	}

	return splitHostFileLines(canonicalHostFileInlineContent(content))
}

func canonicalHostFileInlineContent(content string) string {
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func upsertManagedBlock(content string, fileBlockName string, blockID string, priority int64, blockContent string) (string, error) {
	lines := splitHostFileLines(content)
	fileStart, fileEnd, ok, err := findFileBlockRange(lines, fileBlockName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("managed file block %q was not found", fileBlockName)
	}

	managedStart, managedEnd, ok, err := findManagedBlockRange(lines[fileStart+1:fileEnd], blockID)
	if err != nil {
		return "", err
	}

	rendered := splitHostFileLines(renderManagedBlock(blockID, priority, blockContent))
	if ok {
		managedStart += fileStart + 1
		managedEnd += fileStart + 1
		lines = replaceLines(lines, managedStart, managedEnd+1, rendered)
	} else {
		lines = replaceLines(lines, fileEnd, fileEnd, rendered)
	}

	lines, err = sortManagedBlocksInFileBlock(lines, fileBlockName)
	if err != nil {
		return "", err
	}

	return strings.Join(lines, ""), nil
}

func removeManagedBlock(content string, fileBlockName string, blockID string) (string, error) {
	lines := splitHostFileLines(content)
	fileStart, fileEnd, ok, err := findFileBlockRange(lines, fileBlockName)
	if err != nil {
		return "", err
	}
	if !ok {
		return content, nil
	}

	managedStart, managedEnd, ok, err := findManagedBlockRange(lines[fileStart+1:fileEnd], blockID)
	if err != nil {
		return "", err
	}
	if !ok {
		return content, nil
	}

	managedStart += fileStart + 1
	managedEnd += fileStart + 1
	lines = replaceLines(lines, managedStart, managedEnd+1, nil)

	lines, err = sortManagedBlocksInFileBlock(lines, fileBlockName)
	if err != nil {
		return "", err
	}

	return strings.Join(lines, ""), nil
}

func extractManagedBlockBody(content string, fileBlockName string, blockID string) (string, int64, bool, error) {
	lines := splitHostFileLines(content)
	fileStart, fileEnd, ok, err := findFileBlockRange(lines, fileBlockName)
	if err != nil || !ok {
		return "", 0, ok, err
	}

	managedStart, managedEnd, ok, err := findManagedBlockRange(lines[fileStart+1:fileEnd], blockID)
	if err != nil || !ok {
		return "", 0, ok, err
	}

	block, err := parseManagedBlockLines(lines[fileStart+1+managedStart : fileStart+1+managedEnd+1])
	if err != nil {
		return "", 0, false, err
	}

	return block.body, block.priority, true, nil
}

func extractHostFileBlockInlineContent(lines []string) (string, error) {
	unmanaged := []string{}
	for i := 0; i < len(lines); {
		blockID, ok := parseManagedBlockBegin(lineBody(lines[i]))
		if !ok {
			unmanaged = append(unmanaged, lines[i])
			i++
			continue
		}

		end := findMarkerLine(lines, i+1, managedBlockEndMarker(blockID))
		if end == -1 {
			return "", fmt.Errorf("managed content block %q is missing its end marker", blockID)
		}
		i = end + 1
	}

	return strings.Join(unmanaged, ""), nil
}

func sortManagedBlocksInFileBlock(lines []string, fileBlockName string) ([]string, error) {
	fileStart, fileEnd, ok, err := findFileBlockRange(lines, fileBlockName)
	if err != nil || !ok {
		return lines, err
	}

	sortedInner, err := sortManagedBlocks(lines[fileStart+1 : fileEnd])
	if err != nil {
		return nil, err
	}

	return replaceLines(lines, fileStart+1, fileEnd, sortedInner), nil
}

func sortManagedBlocks(lines []string) ([]string, error) {
	unmanaged := make([]string, 0, len(lines))
	blocks := []hostFileManagedBlock{}

	for i := 0; i < len(lines); {
		blockID, ok := parseManagedBlockBegin(lineBody(lines[i]))
		if !ok {
			unmanaged = append(unmanaged, lines[i])
			i++
			continue
		}

		end := findMarkerLine(lines, i+1, managedBlockEndMarker(blockID))
		if end == -1 {
			return nil, fmt.Errorf("managed content block %q is missing its end marker", blockID)
		}

		block, err := parseManagedBlockLines(lines[i : end+1])
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
		i = end + 1
	}

	sort.SliceStable(blocks, func(i int, j int) bool {
		if blocks[i].priority != blocks[j].priority {
			return blocks[i].priority < blocks[j].priority
		}
		if blocks[i].body != blocks[j].body {
			return blocks[i].body < blocks[j].body
		}

		return blocks[i].id < blocks[j].id
	})

	next := append([]string(nil), unmanaged...)
	for _, block := range blocks {
		next = append(next, splitHostFileLines(renderManagedBlock(block.id, block.priority, block.body))...)
	}

	return next, nil
}

func sortedManagedBlockLines(lines []string) ([]string, error) {
	blocks, err := managedBlocksFromLines(lines)
	if err != nil {
		return nil, err
	}

	sortHostFileManagedBlocks(blocks)

	next := []string{}
	for _, block := range blocks {
		next = append(next, splitHostFileLines(renderManagedBlock(block.id, block.priority, block.body))...)
	}

	return next, nil
}

func managedBlocksFromLines(lines []string) ([]hostFileManagedBlock, error) {
	blocks := []hostFileManagedBlock{}
	for i := 0; i < len(lines); {
		blockID, ok := parseManagedBlockBegin(lineBody(lines[i]))
		if !ok {
			i++
			continue
		}

		end := findMarkerLine(lines, i+1, managedBlockEndMarker(blockID))
		if end == -1 {
			return nil, fmt.Errorf("managed content block %q is missing its end marker", blockID)
		}

		block, err := parseManagedBlockLines(lines[i : end+1])
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
		i = end + 1
	}

	return blocks, nil
}

func sortHostFileManagedBlocks(blocks []hostFileManagedBlock) {
	sort.SliceStable(blocks, func(i int, j int) bool {
		if blocks[i].priority != blocks[j].priority {
			return blocks[i].priority < blocks[j].priority
		}
		if blocks[i].body != blocks[j].body {
			return blocks[i].body < blocks[j].body
		}

		return blocks[i].id < blocks[j].id
	})
}

func parseManagedBlockLines(lines []string) (hostFileManagedBlock, error) {
	if len(lines) < 2 {
		return hostFileManagedBlock{}, fmt.Errorf("managed content block is missing markers")
	}

	blockID, ok := parseManagedBlockBegin(lineBody(lines[0]))
	if !ok {
		return hostFileManagedBlock{}, fmt.Errorf("managed content block is missing its begin marker")
	}
	if lineBody(lines[len(lines)-1]) != managedBlockEndMarker(blockID) {
		return hostFileManagedBlock{}, fmt.Errorf("managed content block %q is missing its end marker", blockID)
	}

	bodyStart := 1
	priority := int64(0)
	if len(lines) > 2 {
		var err error
		var ok bool
		priority, ok, err = parseManagedBlockPriority(lineBody(lines[1]))
		if err != nil {
			return hostFileManagedBlock{}, err
		}
		if ok {
			bodyStart = 2
		}
	}

	return hostFileManagedBlock{
		id:       blockID,
		priority: priority,
		body:     strings.Join(lines[bodyStart:len(lines)-1], ""),
	}, nil
}

func findFileBlockRange(lines []string, name string) (int, int, bool, error) {
	start := findMarkerLine(lines, 0, fileBlockBeginMarker(name))
	if start == -1 {
		return 0, 0, false, nil
	}

	end := findMarkerLine(lines, start+1, fileBlockEndMarker(name))
	if end == -1 {
		return 0, 0, false, fmt.Errorf("managed file block %q is missing its end marker", name)
	}

	return start, end, true, nil
}

func findManagedBlockRange(lines []string, blockID string) (int, int, bool, error) {
	start := findMarkerLine(lines, 0, managedBlockBeginMarker(blockID))
	if start == -1 {
		return 0, 0, false, nil
	}

	end := findMarkerLine(lines, start+1, managedBlockEndMarker(blockID))
	if end == -1 {
		return 0, 0, false, fmt.Errorf("managed content block %q is missing its end marker", blockID)
	}

	return start, end, true, nil
}

func findMarkerLine(lines []string, start int, marker string) int {
	for i := start; i < len(lines); i++ {
		if lineBody(lines[i]) == marker {
			return i
		}
	}

	return -1
}

func splitHostFileLines(content string) []string {
	if content == "" {
		return nil
	}

	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}

	return lines
}

func replaceLines(lines []string, start int, end int, replacement []string) []string {
	next := make([]string, 0, len(lines)-end+start+len(replacement))
	next = append(next, lines[:start]...)
	next = append(next, replacement...)
	next = append(next, lines[end:]...)

	return next
}

func lineBody(line string) string {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")

	return line
}

func renderManagedBlock(blockID string, priority int64, content string) string {
	var builder strings.Builder
	builder.WriteString(managedBlockBeginMarker(blockID))
	builder.WriteString("\n")
	if priority != 0 {
		builder.WriteString(managedBlockPriorityMarker(priority))
		builder.WriteString("\n")
	}
	builder.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(managedBlockEndMarker(blockID))
	builder.WriteString("\n")

	return builder.String()
}

func canonicalManagedBlockBody(content string) string {
	if strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func canonicalHostFileContent(content string) string {
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func trimRenderedManagedBlockBody(content string) string {
	return strings.TrimSuffix(content, "\n")
}

func fileBlockBeginMarker(name string) string {
	return hostFileBlockBeginPrefix + name
}

func fileBlockEndMarker(name string) string {
	return hostFileBlockEndPrefix + name
}

func managedBlockBeginMarker(blockID string) string {
	return hostFileManagedBlockBeginPrefix + blockID
}

func managedBlockEndMarker(blockID string) string {
	return hostFileManagedBlockEndPrefix + blockID
}

func managedBlockPriorityMarker(priority int64) string {
	return hostFileManagedBlockPriority + strconv.FormatInt(priority, 10)
}

func parseFileBlockBegin(line string) (string, bool) {
	name, ok := strings.CutPrefix(line, hostFileBlockBeginPrefix)
	if !ok {
		return "", false
	}

	return name, true
}

func parseManagedBlockBegin(line string) (string, bool) {
	blockID, ok := strings.CutPrefix(line, hostFileManagedBlockBeginPrefix)
	if !ok {
		return "", false
	}

	return blockID, true
}

func parseManagedBlockPriority(line string) (int64, bool, error) {
	priority, ok := strings.CutPrefix(line, hostFileManagedBlockPriority)
	if !ok {
		return 0, false, nil
	}

	value, err := strconv.ParseInt(priority, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("managed content block priority %q is invalid: %w", priority, err)
	}

	return value, true, nil
}

func sortedHostFileBlockNames(names []string) []string {
	next := append([]string(nil), names...)
	sort.Strings(next)

	return next
}

func validateHostFileBlockNames(names []string) error {
	seen := map[string]struct{}{}
	for _, name := range names {
		if err := validateHostFileBlockName(name); err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("file block %q is declared more than once", name)
		}
		seen[name] = struct{}{}
	}

	return nil
}

func validateHostFileBlockName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("file block name must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("file block name must not contain newlines")
	}

	return nil
}

func validateHostFileManagedBlockID(blockID string) error {
	if strings.TrimSpace(blockID) != blockID || blockID == "" {
		return fmt.Errorf("managed content block ID must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(blockID, "\r\n") {
		return fmt.Errorf("managed content block ID must not contain newlines")
	}

	return nil
}

func expandHostPath(path string) (string, error) {
	if strings.TrimSpace(path) != path || path == "" {
		return "", fmt.Errorf("path must be non-empty and must not contain leading or trailing whitespace")
	}

	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("path %q uses unsupported ~user expansion", path)
	}

	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve absolute path for %q: %w", path, err)
		}
		path = absolute
	}

	return filepath.Clean(path), nil
}
