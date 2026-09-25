package provider

import (
	"reflect"
	"testing"
)

// macOSPlistDocument wraps a property list fragment in the document header that
// `defaults export` prints.
func macOSPlistDocument(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>` + body + `</dict>
</plist>
`
}

func TestMacOSPlistXMLRoundTrip(t *testing.T) {
	t.Parallel()

	value := macOSDefaultDictValue([]macOSDefaultDictEntry{
		{Key: "64", Value: macOSDefaultDictValue([]macOSDefaultDictEntry{
			{Key: "enabled", Value: macOSDefaultValue{Type: macOSDefaultValueBool, Bool: false}},
			{Key: "value", Value: macOSDefaultDictValue([]macOSDefaultDictEntry{
				{Key: "parameters", Value: macOSDefaultValue{Type: macOSDefaultValueArray, Array: []macOSDefaultValue{
					{Type: macOSDefaultValueInt, Int: 65535},
					{Type: macOSDefaultValueInt, Int: 49},
					{Type: macOSDefaultValueFloat, Float: 1.5},
				}}},
				{Key: "type", Value: macOSDefaultValue{Type: macOSDefaultValueString, String: "standard <&> \"quoted\""}},
			})},
		})},
		{Key: "languages", Value: macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: []string{"ko-KR", "en-US"}}},
	})

	document, err := macOSPlistXML(value)
	if err != nil {
		t.Fatalf("macOSPlistXML: %s", err)
	}

	decoded, err := parseMacOSPlistXML([]byte(macOSPlistDocument("<key>root</key>" + document)))
	if err != nil {
		t.Fatalf("parseMacOSPlistXML: %s", err)
	}

	root, ok := macOSPlistLookup(decoded, "root")
	if !ok {
		t.Fatal("expected the document to contain root")
	}
	if !macOSDefaultValuesEqual(root, value) {
		t.Fatalf("round trip changed the value:\ngot  %#v\nwant %#v", root, value)
	}
}

func TestMacOSPlistArrayKeepsStringListRepresentation(t *testing.T) {
	t.Parallel()

	decoded, err := parseMacOSPlistXML([]byte(macOSPlistDocument(
		`<key>AppleLanguages</key><array><string>en-US</string><string>ko-KR</string></array>`)))
	if err != nil {
		t.Fatalf("parseMacOSPlistXML: %s", err)
	}

	value, ok := macOSPlistLookup(decoded, "AppleLanguages")
	if !ok {
		t.Fatal("expected the document to contain AppleLanguages")
	}
	if value.Type != macOSDefaultValueStringList {
		t.Fatalf("got type %q, want %q", value.Type, macOSDefaultValueStringList)
	}
	if !reflect.DeepEqual(value.StringList, []string{"en-US", "ko-KR"}) {
		t.Fatalf("got %#v", value.StringList)
	}
}

func TestMacOSPlistSortsDictionaryEntries(t *testing.T) {
	t.Parallel()

	decoded, err := parseMacOSPlistXML([]byte(macOSPlistDocument(
		`<key>hotkeys</key><dict><key>64</key><true/><key>10</key><false/></dict>`)))
	if err != nil {
		t.Fatalf("parseMacOSPlistXML: %s", err)
	}

	value, ok := macOSPlistLookup(decoded, "hotkeys")
	if !ok {
		t.Fatal("expected the document to contain hotkeys")
	}
	if len(value.Dict) != 2 || value.Dict[0].Key != "10" || value.Dict[1].Key != "64" {
		t.Fatalf("got %#v, want entries sorted by key", value.Dict)
	}
}

func TestMacOSPlistReportsUnsupportedElements(t *testing.T) {
	t.Parallel()

	decoded, err := parseMacOSPlistXML([]byte(macOSPlistDocument(
		`<key>managed</key><dict><key>blob</key><data>aGk=</data></dict><key>other</key><string>fine</string>`)))
	if err != nil {
		t.Fatalf("parseMacOSPlistXML: %s", err)
	}

	managed, ok := macOSPlistLookup(decoded, "managed")
	if !ok {
		t.Fatal("expected the document to contain managed")
	}
	if element := macOSPlistUnsupportedElement(managed); element != "data" {
		t.Fatalf("got %q, want data", element)
	}

	// An unsupported element elsewhere in the domain must not disqualify the
	// key that is actually managed.
	other, ok := macOSPlistLookup(decoded, "other")
	if !ok {
		t.Fatal("expected the document to contain other")
	}
	if element := macOSPlistUnsupportedElement(other); element != "" {
		t.Fatalf("got %q, want no unsupported element", element)
	}
}

func TestMacOSDefaultProjectDictKeepsManagedEntriesOnly(t *testing.T) {
	t.Parallel()

	actual := macOSDefaultDictValue([]macOSDefaultDictEntry{
		{Key: "10", Value: macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}},
		{Key: "64", Value: macOSDefaultValue{Type: macOSDefaultValueBool, Bool: false}},
		{Key: "65", Value: macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}},
	})
	configured := macOSDefaultDictValue([]macOSDefaultDictEntry{
		{Key: "64", Value: macOSDefaultValue{Type: macOSDefaultValueBool, Bool: false}},
	})

	projected := macOSDefaultProjectDict(actual, configured)
	if len(projected.Dict) != 1 || projected.Dict[0].Key != "64" {
		t.Fatalf("got %#v, want only the managed entry", projected.Dict)
	}
	if !macOSDefaultValuesEqual(projected, configured) {
		t.Fatal("expected the projected value to match the configured value")
	}
}
