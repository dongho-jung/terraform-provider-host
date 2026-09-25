package provider

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type testFontName struct {
	platform uint16
	encoding uint16
	nameID   uint16
	value    string
}

// buildTestFont writes the smallest sfnt that carries a name table, so the
// parser is exercised without shipping a font binary.
func buildTestFont(names []testFontName) []byte {
	var records []byte
	var storage []byte
	for _, name := range names {
		encoded := []byte(name.value)
		if name.platform != 1 {
			units := utf16.Encode([]rune(name.value))
			encoded = make([]byte, 0, len(units)*2)
			for _, unit := range units {
				encoded = binary.BigEndian.AppendUint16(encoded, unit)
			}
		}
		records = binary.BigEndian.AppendUint16(records, name.platform)
		records = binary.BigEndian.AppendUint16(records, name.encoding)
		records = binary.BigEndian.AppendUint16(records, 0)
		records = binary.BigEndian.AppendUint16(records, name.nameID)
		records = binary.BigEndian.AppendUint16(records, uint16(len(encoded)))
		records = binary.BigEndian.AppendUint16(records, uint16(len(storage)))
		storage = append(storage, encoded...)
	}

	var table []byte
	table = binary.BigEndian.AppendUint16(table, 0)
	table = binary.BigEndian.AppendUint16(table, uint16(len(names)))
	table = binary.BigEndian.AppendUint16(table, uint16(6+len(records)))
	table = append(table, records...)
	table = append(table, storage...)

	const headerLength = 12 + 16
	var font []byte
	font = binary.BigEndian.AppendUint32(font, sfntVersionTrueType)
	font = binary.BigEndian.AppendUint16(font, 1)
	font = binary.BigEndian.AppendUint16(font, 0)
	font = binary.BigEndian.AppendUint16(font, 0)
	font = binary.BigEndian.AppendUint16(font, 0)
	font = binary.BigEndian.AppendUint32(font, sfntNameTableTag)
	font = binary.BigEndian.AppendUint32(font, 0)
	font = binary.BigEndian.AppendUint32(font, headerLength)
	font = binary.BigEndian.AppendUint32(font, uint32(len(table)))
	font = append(font, table...)
	return font
}

// rebaseTestFontTables rewrites the single table record buildTestFont emits so
// its offset is absolute, as a font collection requires.
func rebaseTestFontTables(font []byte, base uint32) []byte {
	rebased := make([]byte, len(font))
	copy(rebased, font)
	const offsetField = 12 + 8
	binary.BigEndian.PutUint32(rebased[offsetField:offsetField+4], binary.BigEndian.Uint32(rebased[offsetField:offsetField+4])+base)
	return rebased
}

func writeTestFont(t *testing.T, dir string, name string, names []testFontName) string {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, buildTestFont(names), 0o644); err != nil {
		t.Fatalf("write font: %s", err)
	}
	return file
}

func TestReadMacOSFontFacesPrefersWindowsUnicodeNames(t *testing.T) {
	t.Parallel()

	file := writeTestFont(t, t.TempDir(), "Test.ttf", []testFontName{
		{platform: 1, encoding: 0, nameID: sfntNameIDPostScript, value: "Legacy-Regular"},
		{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "InconsolataNFM-Regular"},
		{platform: 3, encoding: 1, nameID: sfntNameIDFamily, value: "Inconsolata NFM"},
		{platform: 3, encoding: 1, nameID: sfntNameIDTypographicFamily, value: "Inconsolata Nerd Font Mono"},
	})

	faces, err := readMacOSFontFaces(file)
	if err != nil {
		t.Fatalf("readMacOSFontFaces: %s", err)
	}
	if len(faces) != 1 {
		t.Fatalf("got %d faces, want 1", len(faces))
	}
	if faces[0].PostScriptName != "InconsolataNFM-Regular" {
		t.Fatalf("got %q, want InconsolataNFM-Regular", faces[0].PostScriptName)
	}
	// The typographic family is the name a font picker shows.
	if faces[0].Family != "Inconsolata Nerd Font Mono" {
		t.Fatalf("got %q, want Inconsolata Nerd Font Mono", faces[0].Family)
	}
}

func TestReadMacOSFontFacesFallsBackToMacintoshNames(t *testing.T) {
	t.Parallel()

	file := writeTestFont(t, t.TempDir(), "Legacy.ttf", []testFontName{
		{platform: 1, encoding: 0, nameID: sfntNameIDPostScript, value: "Legacy-Regular"},
		{platform: 1, encoding: 0, nameID: sfntNameIDFamily, value: "Legacy"},
	})

	faces, err := readMacOSFontFaces(file)
	if err != nil {
		t.Fatalf("readMacOSFontFaces: %s", err)
	}
	if faces[0].PostScriptName != "Legacy-Regular" || faces[0].Family != "Legacy" {
		t.Fatalf("got %#v", faces[0])
	}
}

func TestReadMacOSFontFacesRejectsFontWithoutPostScriptName(t *testing.T) {
	t.Parallel()

	file := writeTestFont(t, t.TempDir(), "Nameless.ttf", []testFontName{
		{platform: 3, encoding: 1, nameID: sfntNameIDFamily, value: "Nameless"},
	})

	if _, err := readMacOSFontFaces(file); err == nil {
		t.Fatal("expected a font without a PostScript name to be rejected")
	}
}

func TestParseSfntFacesReadsEveryFaceOfACollection(t *testing.T) {
	t.Parallel()

	first := buildTestFont([]testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "First-Regular"}})
	second := buildTestFont([]testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "Second-Regular"}})

	header := binary.BigEndian.AppendUint32(nil, sfntVersionCollection)
	header = binary.BigEndian.AppendUint32(header, 0x00010000)
	header = binary.BigEndian.AppendUint32(header, 2)
	offset := uint32(len(header) + 8)
	header = binary.BigEndian.AppendUint32(header, offset)
	header = binary.BigEndian.AppendUint32(header, offset+uint32(len(first)))
	// A collection stores table offsets from the start of the file, not from
	// the start of the face that owns them.
	collection := append(header, rebaseTestFontTables(first, offset)...)
	collection = append(collection, rebaseTestFontTables(second, offset+uint32(len(first)))...)

	faces, err := parseSfntFaces(strings.NewReader(string(collection)), "Collection.ttc")
	if err != nil {
		t.Fatalf("parseSfntFaces: %s", err)
	}
	if len(faces) != 2 || faces[0].PostScriptName != "First-Regular" || faces[1].PostScriptName != "Second-Regular" {
		t.Fatalf("got %#v", faces)
	}
}

func TestMacOSFontFilesAtReadsDirectoryWithoutDescending(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFont(t, dir, "B.ttf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "B-Regular"}})
	writeTestFont(t, dir, "A.otf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "A-Regular"}})
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not a font"), 0o644); err != nil {
		t.Fatalf("write file: %s", err)
	}
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %s", err)
	}
	writeTestFont(t, nested, "C.ttf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "C-Regular"}})

	files, err := macOSFontFilesAt(dir)
	if err != nil {
		t.Fatalf("macOSFontFilesAt: %s", err)
	}
	if len(files) != 2 || filepath.Base(files[0]) != "A.otf" || filepath.Base(files[1]) != "B.ttf" {
		t.Fatalf("got %#v", files)
	}

	if _, err := macOSFontFilesAt(nested + "-missing"); err == nil {
		t.Fatal("expected a missing path to be rejected")
	}
	if _, err := macOSFontFilesAt(filepath.Join(dir, "README.txt")); err != nil {
		t.Fatalf("a file is taken as given: %s", err)
	}
}

type fakeMacOSFontManager struct {
	active          map[string]bool
	activeAfterKill map[string]bool
	cleared         [][]string
	restarts        int
	probes          int
	probeErr        error
	clearErr        error
	restartErr      error
}

func (f *fakeMacOSFontManager) ActivePostScriptNames(ctx context.Context, names []string) ([]string, error) {
	f.probes++
	if f.probeErr != nil {
		return nil, f.probeErr
	}
	lookup := f.active
	if f.restarts > 0 && f.activeAfterKill != nil {
		lookup = f.activeAfterKill
	}
	active := make([]string, 0, len(names))
	for _, name := range names {
		if lookup[name] {
			active = append(active, name)
		}
	}
	return active, nil
}

func (f *fakeMacOSFontManager) ClearQuarantine(ctx context.Context, files []string) error {
	if f.clearErr != nil {
		return f.clearErr
	}
	f.cleared = append(f.cleared, files)
	return nil
}

func (f *fakeMacOSFontManager) RestartFontDaemon(ctx context.Context) error {
	if f.restartErr != nil {
		return f.restartErr
	}
	f.restarts++
	return nil
}

func testMacOSFontModel(dir string) MacOSFontResourceModel {
	return MacOSFontResourceModel{
		Path:              types.StringValue(dir),
		ClearQuarantine:   types.BoolValue(true),
		RestartFontDaemon: types.BoolValue(true),
	}
}

func TestMacOSFontActivateRestartsTheDaemonOnlyWhenAFontIsMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFont(t, dir, "Test.ttf", []testFontName{
		{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "Test-Regular"},
		{platform: 3, encoding: 1, nameID: sfntNameIDFamily, value: "Test"},
	})

	manager := &fakeMacOSFontManager{active: map[string]bool{"Test-Regular": true}}
	font := &MacOSFontResource{manager: manager}

	var diags diag.Diagnostics
	state := font.activate(context.Background(), testMacOSFontModel(dir), &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if manager.restarts != 0 {
		t.Fatalf("restarted the font daemon %d times for an active font", manager.restarts)
	}
	if len(manager.cleared) != 1 || len(manager.cleared[0]) != 1 {
		t.Fatalf("got %#v, want the quarantine cleared once", manager.cleared)
	}
	if !state.Active.ValueBool() {
		t.Fatal("expected the font to be reported as active")
	}
	if state.ID.ValueString() != dir || state.PathResolved.ValueString() != dir {
		t.Fatalf("got id %q and path_resolved %q, want %q", state.ID.ValueString(), state.PathResolved.ValueString(), dir)
	}

	names := make([]string, 0, 1)
	state.PostScriptNames.ElementsAs(context.Background(), &names, false)
	if len(names) != 1 || names[0] != "Test-Regular" {
		t.Fatalf("got %#v", names)
	}
	families := make([]string, 0, 1)
	state.Families.ElementsAs(context.Background(), &families, false)
	if len(families) != 1 || families[0] != "Test" {
		t.Fatalf("got %#v", families)
	}
}

func TestMacOSFontActivateRegistersAQuarantinedFontAfterRestartingTheDaemon(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFont(t, dir, "Test.ttf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "Test-Regular"}})

	manager := &fakeMacOSFontManager{
		active:          map[string]bool{},
		activeAfterKill: map[string]bool{"Test-Regular": true},
	}
	font := &MacOSFontResource{manager: manager}

	var diags diag.Diagnostics
	state := font.activate(context.Background(), testMacOSFontModel(dir), &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if manager.restarts != 1 {
		t.Fatalf("restarted the font daemon %d times, want 1", manager.restarts)
	}
	if !state.Active.ValueBool() {
		t.Fatal("expected the font to be active after the restart")
	}
}

func TestMacOSFontActivateFailsWhenMacOSStillRefusesTheFont(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFont(t, dir, "Test.ttf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "Test-Regular"}})

	manager := &fakeMacOSFontManager{active: map[string]bool{}}
	font := &MacOSFontResource{manager: manager}

	var diags diag.Diagnostics
	state := font.activate(context.Background(), testMacOSFontModel(dir), &diags)
	if !diags.HasError() {
		t.Fatal("expected an unregistered font to fail the apply")
	}
	if !strings.Contains(diagnosticsError(diags).Error(), "Test-Regular") {
		t.Fatalf("diagnostic does not name the font: %s", diagnosticsError(diags))
	}
	if state.Active.ValueBool() {
		t.Fatal("expected active to be false")
	}
}

func TestMacOSFontActivateSkipsQuarantineWhenDisabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFont(t, dir, "Test.ttf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "Test-Regular"}})

	manager := &fakeMacOSFontManager{active: map[string]bool{"Test-Regular": true}}
	font := &MacOSFontResource{manager: manager}

	model := testMacOSFontModel(dir)
	model.ClearQuarantine = types.BoolValue(false)
	model.RestartFontDaemon = types.BoolValue(false)

	var diags diag.Diagnostics
	if font.activate(context.Background(), model, &diags); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if len(manager.cleared) != 0 {
		t.Fatalf("cleared the quarantine attribute despite clear_quarantine = false: %#v", manager.cleared)
	}
}

func TestMacOSFontActivateReportsAProbeFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFont(t, dir, "Test.ttf", []testFontName{{platform: 3, encoding: 1, nameID: sfntNameIDPostScript, value: "Test-Regular"}})

	manager := &fakeMacOSFontManager{probeErr: fmt.Errorf("osascript failed")}
	font := &MacOSFontResource{manager: manager}

	var diags diag.Diagnostics
	font.activate(context.Background(), testMacOSFontModel(dir), &diags)
	if !strings.Contains(diagnosticsError(diags).Error(), "osascript failed") {
		t.Fatalf("got %s", diagnosticsError(diags))
	}
}

func TestCLIMacOSFontManagerClearsOnlyQuarantinedFiles(t *testing.T) {
	t.Parallel()

	var commands [][]string
	manager := &CLIMacOSFontManager{
		xattrPath: "/usr/bin/xattr",
		run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
			commands = append(commands, append([]string{command}, args...))
			if len(args) == 1 && args[0] == "/fonts/clean.ttf" {
				return []byte("com.apple.provenance\n"), nil
			}
			if len(args) == 1 {
				return []byte("com.apple.provenance\ncom.apple.quarantine\n"), nil
			}
			return nil, nil
		},
	}

	if err := manager.ClearQuarantine(context.Background(), []string{"/fonts/clean.ttf", "/fonts/quarantined.ttf"}); err != nil {
		t.Fatalf("ClearQuarantine: %s", err)
	}
	if len(commands) != 3 {
		t.Fatalf("got %#v, want a read for each file and one delete", commands)
	}
	deleted := commands[2]
	if deleted[1] != "-d" || deleted[2] != macOSFontQuarantineAttribute || deleted[3] != "/fonts/quarantined.ttf" {
		t.Fatalf("got %#v", deleted)
	}
}

func TestCLIMacOSFontManagerRestartToleratesAStoppedDaemon(t *testing.T) {
	t.Parallel()

	manager := &CLIMacOSFontManager{
		killallPath: "/usr/bin/killall",
		run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
			return nil, fmt.Errorf("killall fontd failed: exit status 1\nNo matching processes belonging to you were found")
		},
	}
	if err := manager.RestartFontDaemon(context.Background()); err != nil {
		t.Fatalf("RestartFontDaemon: %s", err)
	}

	manager.run = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("killall fontd failed: operation not permitted")
	}
	if err := manager.RestartFontDaemon(context.Background()); err == nil {
		t.Fatal("expected a real killall failure to be reported")
	}
}

func TestCLIMacOSFontManagerReadsTheActiveNames(t *testing.T) {
	t.Parallel()

	var passed []string
	manager := &CLIMacOSFontManager{
		osascriptPath: "/usr/bin/osascript",
		run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
			passed = args
			return []byte("Test-Regular\n\n"), nil
		},
	}

	active, err := manager.ActivePostScriptNames(context.Background(), []string{"Test-Regular", "Missing-Regular"})
	if err != nil {
		t.Fatalf("ActivePostScriptNames: %s", err)
	}
	if len(active) != 1 || active[0] != "Test-Regular" {
		t.Fatalf("got %#v", active)
	}
	if len(passed) != 6 || passed[0] != "-l" || passed[1] != "JavaScript" || passed[2] != "-e" {
		t.Fatalf("got %#v", passed)
	}
	if passed[4] != "Test-Regular" || passed[5] != "Missing-Regular" {
		t.Fatalf("names are not passed as arguments: %#v", passed)
	}
}
