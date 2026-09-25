package provider

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

// macOS activates these font containers from a font directory. Suitcase fonts
// (.dfont) keep their tables in a resource fork and are not read here.
var macOSFontExtensions = map[string]struct{}{
	".ttf": {},
	".ttc": {},
	".otf": {},
	".otc": {},
}

const (
	sfntVersionTrueType   uint32 = 0x00010000
	sfntVersionTrue       uint32 = 0x74727565 // 'true'
	sfntVersionOpenType   uint32 = 0x4f54544f // 'OTTO'
	sfntVersionCollection uint32 = 0x74746366 // 'ttcf'

	sfntNameTableTag uint32 = 0x6e616d65 // 'name'

	// Name identifiers defined by the OpenType naming table.
	sfntNameIDFamily            uint16 = 1
	sfntNameIDPostScript        uint16 = 6
	sfntNameIDTypographicFamily uint16 = 16

	// A font collection holds one sfnt per face; the cap only rejects a
	// malformed header claiming an implausible count.
	sfntMaxFacesPerFile = 256
	sfntMaxNameLength   = 1 << 12
)

// macOSFontFace is one face declared by a font file. macOS resolves a font by
// its PostScript name, which is the name a preference such as iTerm's
// "Normal Font" stores, so that is what the resource reports.
type macOSFontFace struct {
	File           string
	PostScriptName string
	Family         string
}

// macOSFontFilesAt lists the font files a configured path covers. A file is
// taken as given; a directory is scanned without descending, because macOS
// activates a font directory itself and not its subdirectories.
func macOSFontFilesAt(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", path, err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := macOSFontExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			continue
		}
		files = append(files, filepath.Join(path, entry.Name()))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no font file in %s; expected one of %s", path, macOSFontExtensionList())
	}
	return files, nil
}

func macOSFontExtensionList() string {
	extensions := make([]string, 0, len(macOSFontExtensions))
	for extension := range macOSFontExtensions {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)
	return strings.Join(extensions, ", ")
}

// readMacOSFontFaces reads the faces a font file declares.
func readMacOSFontFaces(file string) ([]macOSFontFace, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", file, err)
	}
	defer handle.Close()

	faces, err := parseSfntFaces(handle, file)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", file, err)
	}
	return faces, nil
}

func parseSfntFaces(reader io.ReaderAt, file string) ([]macOSFontFace, error) {
	version, err := sfntUint32(reader, 0)
	if err != nil {
		return nil, err
	}

	offsets := []int64{0}
	if version == sfntVersionCollection {
		count, err := sfntUint32(reader, 8)
		if err != nil {
			return nil, err
		}
		if count == 0 || count > sfntMaxFacesPerFile {
			return nil, fmt.Errorf("font collection declares %d fonts", count)
		}
		offsets = offsets[:0]
		for index := uint32(0); index < count; index++ {
			offset, err := sfntUint32(reader, int64(12+4*index))
			if err != nil {
				return nil, err
			}
			offsets = append(offsets, int64(offset))
		}
	}

	faces := make([]macOSFontFace, 0, len(offsets))
	for _, offset := range offsets {
		face, err := parseSfntFace(reader, offset, file)
		if err != nil {
			return nil, err
		}
		faces = append(faces, face)
	}
	return faces, nil
}

func parseSfntFace(reader io.ReaderAt, offset int64, file string) (macOSFontFace, error) {
	version, err := sfntUint32(reader, offset)
	if err != nil {
		return macOSFontFace{}, err
	}
	switch version {
	case sfntVersionTrueType, sfntVersionTrue, sfntVersionOpenType:
	default:
		return macOSFontFace{}, fmt.Errorf("unsupported font format %#08x", version)
	}

	tableCount, err := sfntUint16(reader, offset+4)
	if err != nil {
		return macOSFontFace{}, err
	}

	for index := uint16(0); index < tableCount; index++ {
		record := offset + 12 + int64(index)*16
		tag, err := sfntUint32(reader, record)
		if err != nil {
			return macOSFontFace{}, err
		}
		if tag != sfntNameTableTag {
			continue
		}
		tableOffset, err := sfntUint32(reader, record+8)
		if err != nil {
			return macOSFontFace{}, err
		}
		return parseSfntNameTable(reader, int64(tableOffset), file)
	}
	return macOSFontFace{}, fmt.Errorf("font has no name table")
}

func parseSfntNameTable(reader io.ReaderAt, offset int64, file string) (macOSFontFace, error) {
	count, err := sfntUint16(reader, offset+2)
	if err != nil {
		return macOSFontFace{}, err
	}
	storage, err := sfntUint16(reader, offset+4)
	if err != nil {
		return macOSFontFace{}, err
	}

	names := map[uint16]sfntName{}
	for index := uint16(0); index < count; index++ {
		record := offset + 6 + int64(index)*12
		fields := make([]uint16, 6)
		for field := range fields {
			value, err := sfntUint16(reader, record+int64(field)*2)
			if err != nil {
				return macOSFontFace{}, err
			}
			fields[field] = value
		}

		platform, encoding, nameID, length, stringOffset := fields[0], fields[1], fields[3], fields[4], fields[5]
		switch nameID {
		case sfntNameIDFamily, sfntNameIDPostScript, sfntNameIDTypographicFamily:
		default:
			continue
		}
		if length == 0 || length > sfntMaxNameLength {
			continue
		}

		priority := sfntNamePriority(platform, encoding)
		if priority == 0 {
			continue
		}
		if current, ok := names[nameID]; ok && current.priority >= priority {
			continue
		}

		raw, err := sfntBytes(reader, offset+int64(storage)+int64(stringOffset), int(length))
		if err != nil {
			return macOSFontFace{}, err
		}
		value := decodeSfntName(platform, raw)
		if value == "" {
			continue
		}
		names[nameID] = sfntName{value: value, priority: priority}
	}

	postScript := names[sfntNameIDPostScript].value
	if postScript == "" {
		return macOSFontFace{}, fmt.Errorf("font declares no PostScript name")
	}
	family := names[sfntNameIDTypographicFamily].value
	if family == "" {
		family = names[sfntNameIDFamily].value
	}
	return macOSFontFace{File: file, PostScriptName: postScript, Family: family}, nil
}

type sfntName struct {
	value    string
	priority int
}

// sfntNamePriority ranks the encodings a name record may use. Windows Unicode
// wins because every font that macOS activates carries it; the Macintosh
// Roman record is the fallback for older files. Anything else is skipped
// rather than guessed at.
func sfntNamePriority(platform uint16, encoding uint16) int {
	switch platform {
	case 3: // Windows
		if encoding == 1 || encoding == 10 {
			return 3
		}
		return 0
	case 0: // Unicode
		return 2
	case 1: // Macintosh
		if encoding == 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func decodeSfntName(platform uint16, raw []byte) string {
	if platform == 1 {
		// Macintosh Roman and ASCII agree over the range a font name uses.
		return strings.TrimSpace(string(raw))
	}
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	units := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		units = append(units, binary.BigEndian.Uint16(raw[index:index+2]))
	}
	return strings.TrimSpace(string(utf16.Decode(units)))
}

func sfntUint16(reader io.ReaderAt, offset int64) (uint16, error) {
	raw, err := sfntBytes(reader, offset, 2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(raw), nil
}

func sfntUint32(reader io.ReaderAt, offset int64) (uint32, error) {
	raw, err := sfntBytes(reader, offset, 4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(raw), nil
}

func sfntBytes(reader io.ReaderAt, offset int64, length int) ([]byte, error) {
	if offset < 0 || length < 0 {
		return nil, fmt.Errorf("font is malformed: read of %d bytes at offset %d", length, offset)
	}
	raw := make([]byte, length)
	if _, err := reader.ReadAt(raw, offset); err != nil {
		return nil, fmt.Errorf("font is malformed: %w", err)
	}
	return raw, nil
}
