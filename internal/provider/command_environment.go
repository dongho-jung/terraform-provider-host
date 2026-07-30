package provider

import "strings"

func environmentWithCLocale(environment []string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key == "LC_ALL" || key == "LANG" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "LC_ALL=C", "LANG=C")
}
