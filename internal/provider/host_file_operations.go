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
	"strings"
)

type hostFileManagedBlock struct {
	id     string
	before []string
	after  []string
	body   string
}

type cleanHostFileState struct {
	Path   string                             `json:"path"`
	Blocks map[string]cleanHostFileBlockState `json:"blocks"`
}

type cleanHostFileBlockState struct {
	Order   int                                       `json:"order,omitempty"`
	Before  []string                                  `json:"before,omitempty"`
	After   []string                                  `json:"after,omitempty"`
	Content string                                    `json:"content,omitempty"`
	Managed map[string]cleanHostFileManagedBlockState `json:"managed,omitempty"`
}

type cleanHostFileManagedBlockState struct {
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
	Content string   `json:"content"`
}

func withLockedHostFileForHome(ctx context.Context, homeDir string, path string, fn func(path string) error) error {
	resolvedPath, err := expandHostPathWithHome(path, homeDir)
	if err != nil {
		return err
	}

	lock, err := lockHostFileContext(ctx, resolvedPath)
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

	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %q before write: %w", path, err)
	}
	if err := writeHostFileAtomically(path, []byte(content), mode); err != nil {
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

func syncCleanHostFileBlocksForRuntime(path string, specs []hostFileBlockSpec, runtimeDir string) error {
	if err := validateHostFileBlockSpecs(specs); err != nil {
		return err
	}

	state, err := plannedCleanHostFileStateForProvider(path, specs, "", runtimeDir)
	if err != nil {
		return err
	}

	return writeCleanHostFileStateAndContentForRuntime(path, state, runtimeDir)
}

func plannedCleanHostFileContentForProvider(path string, specs []hostFileBlockSpec, homeDir string, runtimeDir string) (string, error) {
	state, err := plannedCleanHostFileStateForProvider(path, specs, homeDir, runtimeDir)
	if err != nil {
		return "", err
	}

	return renderCleanHostFileState(state)
}

func plannedCleanHostFileStateForProvider(path string, specs []hostFileBlockSpec, homeDir string, runtimeDir string) (cleanHostFileState, error) {
	if err := validateHostFileBlockSpecs(specs); err != nil {
		return cleanHostFileState{}, err
	}

	resolvedPath, err := expandHostPathWithHome(path, homeDir)
	if err != nil {
		return cleanHostFileState{}, err
	}

	state, err := readCleanHostFileStateForRuntime(resolvedPath, runtimeDir)
	if err != nil {
		return cleanHostFileState{}, err
	}
	state.Path = resolvedPath
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
		block.Order = spec.Order
		block.Before = append([]string(nil), spec.Before...)
		block.After = append([]string(nil), spec.After...)
		if spec.Content != nil {
			block.Content = canonicalHostFileInlineContent(*spec.Content)
		} else {
			block.Content = ""
		}
		state.Blocks[spec.Name] = block
	}

	return state, nil
}

func readRenderedHostFileContentForHome(path string, homeDir string) (string, error) {
	resolvedPath, err := expandHostPathWithHome(path, homeDir)
	if err != nil {
		return "", err
	}

	content, exists, err := readHostFileContent(resolvedPath)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}

	return content, nil
}

func deleteCleanHostFileForRuntime(path string, runtimeDir string) error {
	if err := deleteHostFile(path); err != nil {
		return err
	}

	statePath, err := cleanHostFileStatePathForRuntime(path, runtimeDir)
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

func upsertCleanHostFileManagedBlockWithOrderForRuntime(path string, fileBlockName string, blockID string, before []string, after []string, content string, runtimeDir string) error {
	if err := validateHostFileBlockName(fileBlockName); err != nil {
		return err
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return err
	}

	state, err := readCleanHostFileStateForRuntime(path, runtimeDir)
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
		Before:  append([]string(nil), before...),
		After:   append([]string(nil), after...),
		Content: canonicalManagedBlockBody(content),
	}
	state.Blocks[fileBlockName] = block

	return writeCleanHostFileStateAndContentForRuntime(path, state, runtimeDir)
}

func removeCleanHostFileManagedBlockForRuntime(path string, fileBlockName string, blockID string, runtimeDir string) error {
	if err := validateHostFileBlockName(fileBlockName); err != nil {
		return err
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return err
	}

	state, exists, err := readCleanHostFileStateIfExistsForRuntime(path, runtimeDir)
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

	return writeCleanHostFileStateAndContentForRuntime(path, state, runtimeDir)
}

func readCleanManagedBlockForRuntime(path string, fileBlockName string, blockID string, runtimeDir string) (hostFileManagedBlock, bool, error) {
	state, exists, err := readCleanHostFileStateIfExistsForRuntime(path, runtimeDir)
	if err != nil || !exists {
		return hostFileManagedBlock{}, false, err
	}

	block, ok := state.Blocks[fileBlockName]
	if !ok {
		return hostFileManagedBlock{}, false, nil
	}
	managed, ok := block.Managed[blockID]
	if !ok {
		return hostFileManagedBlock{}, false, nil
	}

	return hostFileManagedBlock{
		id:     blockID,
		before: append([]string(nil), managed.Before...),
		after:  append([]string(nil), managed.After...),
		body:   managed.Content,
	}, true, nil
}

func readCleanHostFileBlockSpecsForRuntime(path string, specs []hostFileBlockSpec, runtimeDir string) ([]hostFileBlockSpec, bool, error) {
	state, exists, err := readCleanHostFileStateIfExistsForRuntime(path, runtimeDir)
	if err != nil || !exists {
		return nil, false, err
	}

	next := append([]hostFileBlockSpec(nil), specs...)
	for i, spec := range next {
		block, ok := state.Blocks[spec.Name]
		if !ok {
			return nil, false, nil
		}
		next[i].Order = block.Order
		next[i].Before = append([]string(nil), block.Before...)
		next[i].After = append([]string(nil), block.After...)
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

func readCleanHostFileStateForRuntime(path string, runtimeDir string) (cleanHostFileState, error) {
	state, _, err := readCleanHostFileStateIfExistsForRuntime(path, runtimeDir)
	if err != nil {
		return cleanHostFileState{}, err
	}
	if state.Blocks == nil {
		state.Blocks = map[string]cleanHostFileBlockState{}
	}

	return state, nil
}

func readCleanHostFileStateIfExistsForRuntime(path string, runtimeDir string) (cleanHostFileState, bool, error) {
	statePath, err := cleanHostFileStatePathForRuntime(path, runtimeDir)
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

func writeCleanHostFileStateAndContentForRuntime(path string, state cleanHostFileState, runtimeDir string) error {
	content, err := renderCleanHostFileState(state)
	if err != nil {
		return err
	}
	if err := writeHostFile(path, content); err != nil {
		return err
	}

	statePath, err := cleanHostFileStatePathForRuntime(path, runtimeDir)
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

	if err := writeHostFileAtomically(statePath, data, 0o600); err != nil {
		return fmt.Errorf("write clean host file state %q: %w", statePath, err)
	}

	return nil
}

func renderCleanHostFileState(state cleanHostFileState) (string, error) {
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
	sortedBlocks, err := sortCleanHostFileBlockStateItems(blocks)
	if err != nil {
		return "", err
	}

	sections := []string{}
	for _, item := range sortedBlocks {
		content, err := renderCleanHostFileBlockState(item.block)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		sections = append(sections, content)
	}
	if len(sections) == 0 {
		return "", nil
	}

	return strings.Join(sections, "\n"), nil
}

func renderCleanHostFileBlockState(block cleanHostFileBlockState) (string, error) {
	var builder strings.Builder
	if block.Content != "" {
		builder.WriteString(canonicalHostFileInlineContent(block.Content))
	}

	managed := make([]hostFileManagedBlock, 0, len(block.Managed))
	for id, item := range block.Managed {
		managed = append(managed, hostFileManagedBlock{
			id:     id,
			before: append([]string(nil), item.Before...),
			after:  append([]string(nil), item.After...),
			body:   item.Content,
		})
	}
	if err := sortHostFileManagedBlocks(managed); err != nil {
		return "", err
	}

	for _, item := range managed {
		builder.WriteString(canonicalManagedBlockBody(item.body))
	}

	return builder.String(), nil
}

func sortCleanHostFileBlockStateItems(blocks []struct {
	name  string
	block cleanHostFileBlockState
}) ([]struct {
	name  string
	block cleanHostFileBlockState
}, error) {
	byName := make(map[string]cleanHostFileBlockState, len(blocks))
	for _, item := range blocks {
		byName[item.name] = item.block
	}

	outgoing := make(map[string][]string, len(blocks))
	indegree := make(map[string]int, len(blocks))
	for _, item := range blocks {
		indegree[item.name] = 0
	}

	addEdge := func(from string, to string) error {
		if from == to {
			return fmt.Errorf("host file block %q cannot order itself", from)
		}
		if _, ok := byName[from]; !ok {
			return fmt.Errorf("host file block %q references unknown block %q", to, from)
		}
		if _, ok := byName[to]; !ok {
			return fmt.Errorf("host file block %q references unknown block %q", from, to)
		}
		for _, existing := range outgoing[from] {
			if existing == to {
				return nil
			}
		}
		outgoing[from] = append(outgoing[from], to)
		indegree[to]++

		return nil
	}

	for _, item := range blocks {
		for _, after := range item.block.After {
			if err := addEdge(after, item.name); err != nil {
				return nil, err
			}
		}
		for _, before := range item.block.Before {
			if err := addEdge(item.name, before); err != nil {
				return nil, err
			}
		}
	}

	remaining := make(map[string]struct{}, len(blocks))
	for _, item := range blocks {
		remaining[item.name] = struct{}{}
	}

	sortedBlocks := make([]struct {
		name  string
		block cleanHostFileBlockState
	}, 0, len(blocks))
	for len(remaining) > 0 {
		candidates := make([]struct {
			name  string
			block cleanHostFileBlockState
		}, 0, len(remaining))
		for name := range remaining {
			if indegree[name] == 0 {
				candidates = append(candidates, struct {
					name  string
					block cleanHostFileBlockState
				}{name: name, block: byName[name]})
			}
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("host file block ordering contains a cycle")
		}
		sort.Slice(candidates, func(i int, j int) bool {
			if candidates[i].block.Order != candidates[j].block.Order {
				return candidates[i].block.Order < candidates[j].block.Order
			}

			return candidates[i].name < candidates[j].name
		})

		next := candidates[0]
		sortedBlocks = append(sortedBlocks, next)
		delete(remaining, next.name)
		for _, to := range outgoing[next.name] {
			indegree[to]--
		}
	}

	return sortedBlocks, nil
}

func cleanHostFileStatePathForRuntime(path string, runtimeDir string) (string, error) {
	stateDir, err := providerRuntimeSubdir(runtimeDir, "host_files")
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(path))

	return filepath.Join(stateDir, hex.EncodeToString(sum[:])+".json"), nil
}

func canonicalHostFileInlineContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func sortHostFileManagedBlocks(blocks []hostFileManagedBlock) error {
	byID := make(map[string]hostFileManagedBlock, len(blocks))
	for _, block := range blocks {
		byID[block.id] = block
	}

	outgoing := make(map[string][]string, len(blocks))
	indegree := make(map[string]int, len(blocks))
	for _, block := range blocks {
		indegree[block.id] = 0
	}

	addEdge := func(from string, to string) error {
		if from == to {
			return fmt.Errorf("managed content block %q cannot order itself", from)
		}
		if _, ok := byID[from]; !ok {
			return fmt.Errorf("managed content block %q references unknown block %q", to, from)
		}
		if _, ok := byID[to]; !ok {
			return fmt.Errorf("managed content block %q references unknown block %q", from, to)
		}
		for _, existing := range outgoing[from] {
			if existing == to {
				return nil
			}
		}
		outgoing[from] = append(outgoing[from], to)
		indegree[to]++

		return nil
	}

	for _, block := range blocks {
		for _, after := range block.after {
			if err := addEdge(after, block.id); err != nil {
				return err
			}
		}
		for _, before := range block.before {
			if err := addEdge(block.id, before); err != nil {
				return err
			}
		}
	}

	remaining := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		remaining[block.id] = struct{}{}
	}

	sortedBlocks := make([]hostFileManagedBlock, 0, len(blocks))
	for len(remaining) > 0 {
		candidates := make([]hostFileManagedBlock, 0, len(remaining))
		for id := range remaining {
			if indegree[id] == 0 {
				candidates = append(candidates, byID[id])
			}
		}
		if len(candidates) == 0 {
			return fmt.Errorf("managed content block ordering contains a cycle")
		}
		sort.SliceStable(candidates, func(i int, j int) bool {
			if candidates[i].body != candidates[j].body {
				return candidates[i].body < candidates[j].body
			}

			return candidates[i].id < candidates[j].id
		})

		next := candidates[0]
		sortedBlocks = append(sortedBlocks, next)
		delete(remaining, next.id)
		for _, to := range outgoing[next.id] {
			indegree[to]--
		}
	}

	copy(blocks, sortedBlocks)

	return nil
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

func canonicalManagedBlockBody(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func canonicalHostFileContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func trimRenderedManagedBlockBody(content string) string {
	return strings.TrimSuffix(content, "\n")
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
