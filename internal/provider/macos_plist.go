package provider

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// macOSPlistXML renders value as an XML property list fragment. `defaults
// write` parses such a fragment as the value argument, which is the only way
// to write a dictionary or a non-string array from the command line.
func macOSPlistXML(value macOSDefaultValue) (string, error) {
	var builder strings.Builder
	if err := writeMacOSPlistXML(&builder, value); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func writeMacOSPlistXML(builder *strings.Builder, value macOSDefaultValue) error {
	switch value.Type {
	case macOSDefaultValueBool:
		if value.Bool {
			builder.WriteString("<true/>")
			return nil
		}
		builder.WriteString("<false/>")
		return nil
	case macOSDefaultValueInt:
		builder.WriteString("<integer>")
		builder.WriteString(strconv.FormatInt(value.Int, 10))
		builder.WriteString("</integer>")
		return nil
	case macOSDefaultValueFloat:
		builder.WriteString("<real>")
		builder.WriteString(strconv.FormatFloat(value.Float, 'f', -1, 64))
		builder.WriteString("</real>")
		return nil
	case macOSDefaultValueString:
		return writeMacOSPlistString(builder, value.String)
	case macOSDefaultValueStringList:
		builder.WriteString("<array>")
		for _, element := range value.StringList {
			if err := writeMacOSPlistString(builder, element); err != nil {
				return err
			}
		}
		builder.WriteString("</array>")
		return nil
	case macOSDefaultValueArray:
		builder.WriteString("<array>")
		for _, element := range value.Array {
			if err := writeMacOSPlistXML(builder, element); err != nil {
				return err
			}
		}
		builder.WriteString("</array>")
		return nil
	case macOSDefaultValueDict:
		builder.WriteString("<dict>")
		for _, entry := range value.Dict {
			builder.WriteString("<key>")
			if err := writeMacOSPlistText(builder, entry.Key); err != nil {
				return err
			}
			builder.WriteString("</key>")
			if err := writeMacOSPlistXML(builder, entry.Value); err != nil {
				return err
			}
		}
		builder.WriteString("</dict>")
		return nil
	default:
		return fmt.Errorf("unsupported macOS default value type %q", value.Type)
	}
}

func writeMacOSPlistString(builder *strings.Builder, value string) error {
	builder.WriteString("<string>")
	if err := writeMacOSPlistText(builder, value); err != nil {
		return err
	}
	builder.WriteString("</string>")
	return nil
}

func writeMacOSPlistText(builder *strings.Builder, text string) error {
	return xml.EscapeText(builder, []byte(text))
}

// parseMacOSPlistXML decodes the root value of an XML property list document,
// such as the output of `defaults export <domain> -`.
func parseMacOSPlistXML(document []byte) (macOSDefaultValue, error) {
	decoder := xml.NewDecoder(bytes.NewReader(document))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return macOSDefaultValue{}, fmt.Errorf("property list document contains no value")
		}
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse property list document: %w", err)
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "plist" {
			return decodeMacOSPlistValue(decoder, start)
		}
		return decodeMacOSPlistRoot(decoder)
	}
}

func decodeMacOSPlistRoot(decoder *xml.Decoder) (macOSDefaultValue, error) {
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return macOSDefaultValue{}, fmt.Errorf("property list root element contains no value")
		}
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse property list document: %w", err)
		}

		switch typed := token.(type) {
		case xml.StartElement:
			return decodeMacOSPlistValue(decoder, typed)
		case xml.EndElement:
			return macOSDefaultValue{}, fmt.Errorf("property list root element contains no value")
		}
	}
}

func decodeMacOSPlistValue(decoder *xml.Decoder, start xml.StartElement) (macOSDefaultValue, error) {
	switch start.Name.Local {
	case "true", "false":
		if err := decoder.Skip(); err != nil {
			return macOSDefaultValue{}, err
		}
		return macOSDefaultValue{Type: macOSDefaultValueBool, Bool: start.Name.Local == "true"}, nil
	case "integer":
		text, err := decodeMacOSPlistText(decoder, start)
		if err != nil {
			return macOSDefaultValue{}, err
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse property list integer %q: %w", text, err)
		}
		return macOSDefaultValue{Type: macOSDefaultValueInt, Int: parsed}, nil
	case "real":
		text, err := decodeMacOSPlistText(decoder, start)
		if err != nil {
			return macOSDefaultValue{}, err
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse property list real %q: %w", text, err)
		}
		return macOSDefaultValue{Type: macOSDefaultValueFloat, Float: parsed}, nil
	case "string":
		text, err := decodeMacOSPlistText(decoder, start)
		if err != nil {
			return macOSDefaultValue{}, err
		}
		return macOSDefaultValue{Type: macOSDefaultValueString, String: text}, nil
	case "array":
		return decodeMacOSPlistArray(decoder)
	case "dict":
		return decodeMacOSPlistDict(decoder)
	default:
		// `data` and `date` have no Terraform representation here. Record the
		// element name so the caller can report exactly what it cannot manage
		// instead of silently corrupting the value.
		if err := decoder.Skip(); err != nil {
			return macOSDefaultValue{}, err
		}
		return macOSDefaultValue{Type: macOSDefaultValueUnsupported, String: start.Name.Local}, nil
	}
}

func decodeMacOSPlistArray(decoder *xml.Decoder) (macOSDefaultValue, error) {
	elements := make([]macOSDefaultValue, 0)
	for {
		token, err := decoder.Token()
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse property list array: %w", err)
		}

		switch typed := token.(type) {
		case xml.StartElement:
			element, err := decodeMacOSPlistValue(decoder, typed)
			if err != nil {
				return macOSDefaultValue{}, err
			}
			elements = append(elements, element)
		case xml.EndElement:
			return macOSPlistArrayValue(elements), nil
		}
	}
}

// macOSPlistArrayValue keeps all-string arrays in the string_list
// representation so existing configurations and state keep round-tripping.
func macOSPlistArrayValue(elements []macOSDefaultValue) macOSDefaultValue {
	texts := make([]string, 0, len(elements))
	for _, element := range elements {
		if element.Type != macOSDefaultValueString {
			return macOSDefaultValue{Type: macOSDefaultValueArray, Array: elements}
		}
		texts = append(texts, element.String)
	}
	return macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: texts}
}

func decodeMacOSPlistDict(decoder *xml.Decoder) (macOSDefaultValue, error) {
	entries := make([]macOSDefaultDictEntry, 0)
	pendingKey := ""
	haveKey := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse property list dictionary: %w", err)
		}

		switch typed := token.(type) {
		case xml.StartElement:
			if typed.Name.Local == "key" {
				if haveKey {
					return macOSDefaultValue{}, fmt.Errorf("property list dictionary key %q has no value", pendingKey)
				}
				text, err := decodeMacOSPlistText(decoder, typed)
				if err != nil {
					return macOSDefaultValue{}, err
				}
				pendingKey = text
				haveKey = true
				continue
			}
			if !haveKey {
				return macOSDefaultValue{}, fmt.Errorf("property list dictionary value <%s> has no key", typed.Name.Local)
			}
			value, err := decodeMacOSPlistValue(decoder, typed)
			if err != nil {
				return macOSDefaultValue{}, err
			}
			entries = append(entries, macOSDefaultDictEntry{Key: pendingKey, Value: value})
			pendingKey = ""
			haveKey = false
		case xml.EndElement:
			if haveKey {
				return macOSDefaultValue{}, fmt.Errorf("property list dictionary key %q has no value", pendingKey)
			}
			return macOSDefaultDictValue(entries), nil
		}
	}
}

func decodeMacOSPlistText(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("cannot parse property list <%s>: %w", start.Name.Local, err)
		}

		switch typed := token.(type) {
		case xml.CharData:
			builder.Write(typed)
		case xml.StartElement:
			return "", fmt.Errorf("property list <%s> must not contain <%s>", start.Name.Local, typed.Name.Local)
		case xml.EndElement:
			return builder.String(), nil
		}
	}
}

// macOSDefaultDictValue sorts dictionary entries by key so a dictionary built
// from Terraform configuration and the same dictionary decoded from macOS
// always compare and serialize identically.
func macOSDefaultDictValue(entries []macOSDefaultDictEntry) macOSDefaultValue {
	sorted := make([]macOSDefaultDictEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i int, j int) bool {
		return sorted[i].Key < sorted[j].Key
	})
	return macOSDefaultValue{Type: macOSDefaultValueDict, Dict: sorted}
}

// macOSPlistLookup returns the named entry of a decoded property list
// dictionary, which is how a single defaults key is selected from a whole
// exported domain.
func macOSPlistLookup(document macOSDefaultValue, key string) (macOSDefaultValue, bool) {
	if document.Type != macOSDefaultValueDict {
		return macOSDefaultValue{}, false
	}
	for _, entry := range document.Dict {
		if entry.Key == key {
			return entry.Value, true
		}
	}
	return macOSDefaultValue{}, false
}

// macOSPlistUnsupportedElement reports the first property list element in value
// that has no Terraform representation.
func macOSPlistUnsupportedElement(value macOSDefaultValue) string {
	switch value.Type {
	case macOSDefaultValueUnsupported:
		return value.String
	case macOSDefaultValueArray:
		for _, element := range value.Array {
			if name := macOSPlistUnsupportedElement(element); name != "" {
				return name
			}
		}
	case macOSDefaultValueDict:
		for _, entry := range value.Dict {
			if name := macOSPlistUnsupportedElement(entry.Value); name != "" {
				return name
			}
		}
	}
	return ""
}
