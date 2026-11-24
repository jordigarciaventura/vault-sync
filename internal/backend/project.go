package backend

import (
	"fmt"
	"strings"
	"unicode"
)

// SplitProject separates a provider container from an optional folder prefix.
// Paths are canonical and relative; invalid paths are never silently cleaned.
func SplitProject(project string) (container, prefix string, err error) {
	for _, segment := range strings.Split(project, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.TrimSpace(segment) != segment || strings.Contains(segment, `\`) || strings.IndexFunc(segment, unicode.IsControl) >= 0 {
			return "", "", fmt.Errorf("project: use a relative slash-separated path without empty, '.', '..', backslash or whitespace-padded segments")
		}
	}
	container, prefix, _ = strings.Cut(project, "/")
	return container, prefix, nil
}
