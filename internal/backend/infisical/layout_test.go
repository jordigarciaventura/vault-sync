package infisical

import (
	"errors"
	"testing"
)

func TestLocate(t *testing.T) {
	for _, tc := range []struct {
		in, dir, key string
		ok           bool
	}{
		{".env", "/", ".env", true},
		{"backend/.env", "/backend", ".env", true},
		{"shared/keys/firmware-signing.key", "/shared/keys", "firmware-signing.key", true},
		{"my_app/v-2/id_rsa", "/my_app/v-2", "id_rsa", true},
		{".github/token", "", "", false}, // '.' not allowed in folder names
		{"my dir/x", "", "", false},      // nor spaces
		{"a/b:c", "", "", false},         // ':' not allowed in keys
		{"a/ key", "", "", false},        // key would be trimmed
		{"dir/", "", "", false},          // no key
	} {
		loc, err := locate(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("%q: err = %v", tc.in, err)
			continue
		}
		if tc.ok && (loc.Dir != tc.dir || loc.Key != tc.key) {
			t.Errorf("%q: got %+v", tc.in, loc)
		}
	}
}

func TestLocationString(t *testing.T) {
	if s := (location{Dir: "/", Key: ".env"}).String(); s != "/.env" {
		t.Error(s)
	}
	if s := (location{Dir: "/a/b", Key: "k"}).String(); s != "/a/b/k" {
		t.Error(s)
	}
}

func TestCheckStorable(t *testing.T) {
	for content, ok := range map[string]bool{
		"":                true,
		"\n":              true,
		"A=1":             true,
		"A=1\n":           true,
		"A=1\nB=2\n":      true,
		"multi\n\nline\n": true,  // inner blank lines are fine
		"A=1\r\n":         false, // \r before the final newline is trimmed
		"A=1\n\n":         false,
		" A=1":            false,
		"\tA=1\n":         false,
		"\uFEFFA=1":       false,
		"A=1 ":            false,
		"\xff":            false,
		"a\x00b":          false,
		"ünïcødé ✓\n":     true,
	} {
		err := checkStorable("f", []byte(content))
		if ok != (err == nil) {
			t.Errorf("%q: err = %v", content, err)
		}
		if err != nil && !errors.Is(err, errNotStorable) {
			t.Errorf("%q: err does not wrap errNotStorable: %v", content, err)
		}
	}
}

func TestServerNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"  a  ":   "a",
		"a\n\n":   "a\n",
		" a \n":   "a\n",
		"a\nb\n":  "a\nb\n",
		"\n\na\n": "a\n",
	} {
		if got := serverNormalize(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
