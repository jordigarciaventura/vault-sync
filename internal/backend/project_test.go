package backend

import "testing"

func TestSplitProject(t *testing.T) {
	for _, value := range []string{"", "/root", "root/", "root//child", "root/../child", "root/./child", `root\child`, "root/ child", "root/child\n"} {
		if _, _, err := SplitProject(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, value := range []string{"root", "root/child", "root/child/nested", "My Project/child"} {
		root, prefix, err := SplitProject(value)
		got := root
		if prefix != "" {
			got += "/" + prefix
		}
		if err != nil || got != value {
			t.Errorf("%q: %q %v", value, got, err)
		}
	}
}
