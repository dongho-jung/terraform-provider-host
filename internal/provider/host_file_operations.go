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

const (
	hostFileBlockBeginPrefix        = "# BEGIN Terraform host_file block "
	hostFileBlockEndPrefix          = "# END Terraform host_file block "
	hostFileManagedBlockBeginPrefix = "# BEGIN Terraform host_file_block "
	hostFileManagedBlockEndPrefix   = "# END Terraform host_file_block "
	hostFileManagedBlockBefore      = "# Terraform host_file_block before "
	hostFileManagedBlockAfter       = "# Terraform host_file_block after "
	hostFileManagedBlockPriority    = "# Terraform host_file_block priority "
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

	state, err := plannedCleanHostFileState(path, specs)
	if err != nil {
		return err
	}

	return writeCleanHostFileStateAndContent(path, state)
}

func plannedCleanHostFileContent(path string, specs []hostFileBlockSpec) (string, error) {
	state, err := plannedCleanHostFileState(path, specs)
	if err != nil {
		return "", err
	}

	return renderCleanHostFileState(state)
}

func plannedCleanHostFileState(path string, specs []hostFileBlockSpec) (cleanHostFileState, error) {
	if err := validateHostFileBlockSpecs(specs); err != nil {
		return cleanHostFileState{}, err
	}

	resolvedPath, err := expandHostPath(path)
	if err != nil {
		return cleanHostFileState{}, err
	}

	state, err := readCleanHostFileState(resolvedPath)
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

func readRenderedHostFileContent(path string) (string, error) {
	resolvedPath, err := expandHostPath(path)
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

func upsertCleanHostFileManagedBlock(path string, fileBlockName string, blockID string, content string) error {
	return upsertCleanHostFileManagedBlockWithOrder(path, fileBlockName, blockID, nil, nil, content)
}

func upsertCleanHostFileManagedBlockWithOrder(path string, fileBlockName string, blockID string, before []string, after []string, content string) error {
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
		Before:  append([]string(nil), before...),
		After:   append([]string(nil), after...),
		Content: canonicalManagedBlockBody(content),
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

func readCleanManagedBlockBody(path string, fileBlockName string, blockID string) (string, bool, error) {
	block, ok, err := readCleanManagedBlock(path, fileBlockName, blockID)
	if err != nil || !ok {
		return "", ok, err
	}

	return block.body, true, nil
}

func readCleanManagedBlock(path string, fileBlockName string, blockID string) (hostFileManagedBlock, bool, error) {
	state, exists, err := readCleanHostFileStateIfExists(path)
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
	content, err := renderCleanHostFileState(state)
	if err != nil {
		return err
	}
	if err := writeHostFile(path, content); err != nil {
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

func cleanHostFileStatePath(path string) (string, error) {
	stateDir, err := providerRuntimeSubdir("host_files")
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(path))

	return filepath.Join(stateDir, hex.EncodeToString(sum[:])+".json"), nil
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
	content = strings.TrimSpace(content)
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}

	return content + "\n"
}

func upsertManagedBlock(content string, fileBlockName string, blockID string, blockContent string) (string, error) {
	return upsertManagedBlockWithOrder(content, fileBlockName, blockID, nil, nil, blockContent)
}

func upsertManagedBlockWithOrder(content string, fileBlockName string, blockID string, before []string, after []string, blockContent string) (string, error) {
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

	rendered := splitHostFileLines(renderManagedBlockWithOrder(blockID, before, after, blockContent))
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

func extractManagedBlockBody(content string, fileBlockName string, blockID string) (string, bool, error) {
	block, ok, err := extractManagedBlock(content, fileBlockName, blockID)
	if err != nil || !ok {
		return "", ok, err
	}

	return block.body, true, nil
}

func extractManagedBlock(content string, fileBlockName string, blockID string) (hostFileManagedBlock, bool, error) {
	lines := splitHostFileLines(content)
	fileStart, fileEnd, ok, err := findFileBlockRange(lines, fileBlockName)
	if err != nil || !ok {
		return hostFileManagedBlock{}, ok, err
	}

	managedStart, managedEnd, ok, err := findManagedBlockRange(lines[fileStart+1:fileEnd], blockID)
	if err != nil || !ok {
		return hostFileManagedBlock{}, ok, err
	}

	block, err := parseManagedBlockLines(lines[fileStart+1+managedStart : fileStart+1+managedEnd+1])
	if err != nil {
		return hostFileManagedBlock{}, false, err
	}

	return block, true, nil
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

	if err := sortHostFileManagedBlocks(blocks); err != nil {
		return nil, err
	}

	next := append([]string(nil), unmanaged...)
	for _, block := range blocks {
		next = append(next, splitHostFileLines(renderManagedBlockWithOrder(block.id, block.before, block.after, block.body))...)
	}

	return next, nil
}

func sortedManagedBlockLines(lines []string) ([]string, error) {
	blocks, err := managedBlocksFromLines(lines)
	if err != nil {
		return nil, err
	}

	if err := sortHostFileManagedBlocks(blocks); err != nil {
		return nil, err
	}

	next := []string{}
	for _, block := range blocks {
		next = append(next, splitHostFileLines(renderManagedBlockWithOrder(block.id, block.before, block.after, block.body))...)
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
	var before []string
	var after []string
	for bodyStart < len(lines)-1 {
		line := lineBody(lines[bodyStart])
		_, ok := parseManagedBlockPriority(line)
		if ok {
			bodyStart++
			continue
		}

		parsedBefore, ok := parseManagedBlockReferenceMarker(line, hostFileManagedBlockBefore)
		if ok {
			before = parsedBefore
			bodyStart++
			continue
		}

		parsedAfter, ok := parseManagedBlockReferenceMarker(line, hostFileManagedBlockAfter)
		if ok {
			after = parsedAfter
			bodyStart++
			continue
		}

		break
	}

	return hostFileManagedBlock{
		id:     blockID,
		before: before,
		after:  after,
		body:   strings.Join(lines[bodyStart:len(lines)-1], ""),
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

func renderManagedBlock(blockID string, content string) string {
	return renderManagedBlockWithOrder(blockID, nil, nil, content)
}

func renderManagedBlockWithOrder(blockID string, before []string, after []string, content string) string {
	var builder strings.Builder
	builder.WriteString(managedBlockBeginMarker(blockID))
	builder.WriteString("\n")
	if len(before) > 0 {
		builder.WriteString(managedBlockBeforeMarker(before))
		builder.WriteString("\n")
	}
	if len(after) > 0 {
		builder.WriteString(managedBlockAfterMarker(after))
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

func managedBlockBeforeMarker(before []string) string {
	return hostFileManagedBlockBefore + strings.Join(before, ",")
}

func managedBlockAfterMarker(after []string) string {
	return hostFileManagedBlockAfter + strings.Join(after, ",")
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

func parseManagedBlockPriority(line string) (string, bool) {
	return strings.CutPrefix(line, hostFileManagedBlockPriority)
}

func parseManagedBlockReferenceMarker(line string, prefix string) ([]string, bool) {
	references, ok := strings.CutPrefix(line, prefix)
	if !ok {
		return nil, false
	}
	if references == "" {
		return nil, true
	}

	return strings.Split(references, ","), true
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
