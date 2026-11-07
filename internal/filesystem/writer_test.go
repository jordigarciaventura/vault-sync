package filesystem

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mode(t *testing.T, p string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".vault-sync-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestWriteCreatesDirectoriesAndFile(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "a", "b", ".env")

	st, err := WriteFile(dest, []byte("KEY=1\n"), 0o600, Options{ConfineTo: root})
	if err != nil {
		t.Fatal(err)
	}
	if st != Created {
		t.Errorf("status = %v, want created", st)
	}
	if got := readFile(t, dest); got != "KEY=1\n" {
		t.Errorf("content = %q", got)
	}
	if m := mode(t, dest); m != 0o600 {
		t.Errorf("mode = %o", m)
	}
	if m := mode(t, filepath.Dir(dest)); m != DirPermissions {
		t.Errorf("dir mode = %o", m)
	}
	noTempFiles(t, filepath.Dir(dest))
}

func TestWriteOverwritesAndAppliesPermissions(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "f")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := WriteFile(dest, []byte("new"), 0o640, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st != Updated || readFile(t, dest) != "new" || mode(t, dest) != 0o640 {
		t.Errorf("status=%v content=%q mode=%o", st, readFile(t, dest), mode(t, dest))
	}
}

func TestWriteUnchangedFixesPermissions(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(dest, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := WriteFile(dest, []byte("same"), 0o600, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st != Unchanged || mode(t, dest) != 0o600 {
		t.Errorf("status=%v mode=%o", st, mode(t, dest))
	}
}

func TestWriteIsAtomicReplacement(t *testing.T) {
	// The destination must be replaced (new inode), never written in place,
	// so readers holding the old file never see partial content.
	dest := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(dest)
	if _, err := WriteFile(dest, []byte("new"), 0o600, Options{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(dest)
	if os.SameFile(before, after) {
		t.Error("file was modified in place instead of atomically replaced")
	}
}

func TestWriteRefusesSymlinkDestination(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(link, []byte("x"), 0o600, Options{}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("expected symlink error, got %v", err)
	}
	if readFile(t, target) != "keep" {
		t.Error("symlink target was modified")
	}
}

func TestWriteRefusesSymlinkedDirectoryOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "escape", "nested", "f")
	if _, err := WriteFile(dest, []byte("x"), 0o600, Options{ConfineTo: root}); err == nil || !strings.Contains(err.Error(), "outside the project root") {
		t.Fatalf("expected confinement error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "nested")); err == nil {
		t.Error("directory was created outside the root")
	}
}

func TestWriteAllowsSymlinkedDirectoryInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(filepath.Join(root, "alias", "f"), []byte("x"), 0o600, Options{ConfineTo: root}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAbsoluteWithoutConfinement(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "abs", "key")
	if _, err := WriteFile(dest, []byte("k"), 0o600, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFailureLeavesExistingFileIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "f")
	if err := os.WriteFile(dest, []byte("valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := WriteFile(dest, []byte("new"), 0o600, Options{}); err == nil {
		t.Fatal("expected error writing into read-only directory")
	}
	if readFile(t, dest) != "valid" {
		t.Error("existing file was modified")
	}
	noTempFiles(t, dir)
}

func TestWriteRefusesDirectoryDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(dest, []byte("x"), 0o600, Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestInspect(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "new", "f")
	if st, err := Inspect(missing, Options{ConfineTo: root}); err != nil || st != Created {
		t.Errorf("Inspect(missing) = %v, %v", st, err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); err == nil {
		t.Error("Inspect created a directory")
	}
	existing := filepath.Join(root, "f")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := Inspect(existing, Options{ConfineTo: root}); err != nil || st != Updated {
		t.Errorf("Inspect(existing) = %v, %v", st, err)
	}
}
