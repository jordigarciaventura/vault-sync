package infisical

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// location is where a logical file lives inside an Infisical environment.
type location struct {
	// Dir is the secret path (folder), e.g. "/" or "/backend".
	Dir string
	// Key is the secret name, e.g. ".env".
	Key string
}

func (l location) String() string {
	if l.Dir == "/" {
		return "/" + l.Key
	}
	return l.Dir + "/" + l.Key
}

// locate maps a logical filename to a folder and secret key: the directory
// part becomes the Infisical folder path and the base name the secret key, so
// "backend/.env" is the secret ".env" in folder "/backend".
func locate(filename string) (location, error) {
	dir, key := path.Split(filename)
	dir = "/" + strings.Trim(dir, "/")

	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if seg != "" && !validFolderName(seg) {
			return location{}, fmt.Errorf("%q: directory %q cannot be an Infisical folder (only letters, digits, '-' and '_' are allowed); rename the directory", filename, seg)
		}
	}
	switch {
	case key == "":
		return location{}, fmt.Errorf("%q does not name a file", filename)
	case strings.Contains(key, ":"):
		return location{}, fmt.Errorf("%q: Infisical secret names cannot contain ':'; rename the file", filename)
	case strings.TrimFunc(key, isTrimmed) != key:
		return location{}, fmt.Errorf("%q: Infisical trims whitespace around secret names; rename the file", filename)
	}
	return location{Dir: dir, Key: key}, nil
}

// validFolderName mirrors Infisical's folder name rule.
func validFolderName(s string) bool {
	if s == "" {
		return false
	}
	return strings.Trim(s, folderNameChars) == ""
}

const folderNameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

// isTrimmed reports whether JavaScript's String.prototype.trim (used by the
// Infisical server) removes r.
func isTrimmed(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

// errNotStorable is wrapped by checkStorable errors.
var errNotStorable = errors.New("cannot be stored in Infisical without changing it")

// checkStorable makes sure content survives a round trip through Infisical.
// Secret values are text, and the server trims surrounding whitespace (keeping
// a single trailing newline), so such files would come back different.
func checkStorable(filename string, content []byte) error {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return fmt.Errorf("%q %w: secret values must be UTF-8 text without NUL bytes", filename, errNotStorable)
	}
	if s := string(content); serverNormalize(s) != s {
		return fmt.Errorf("%q %w: Infisical trims whitespace at both ends of a value (keeping one final newline); remove leading whitespace, trailing spaces or blank lines, or convert CRLF line endings to LF", filename, errNotStorable)
	}
	return nil
}

// serverNormalize reproduces the transformation the Infisical API applies to
// every secret value it stores.
func serverNormalize(s string) string {
	if strings.HasSuffix(s, "\n") {
		return strings.TrimFunc(s, isTrimmed) + "\n"
	}
	return strings.TrimFunc(s, isTrimmed)
}
