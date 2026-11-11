package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildLauncherFileIndexSkipsAndDepth(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"))
	mustWriteFile(t, filepath.Join(root, "sub", "b.txt"))
	mustWriteFile(t, filepath.Join(root, "sub", "deep", "c.txt"))
	mustWriteFile(t, filepath.Join(root, ".hidden", "secret.txt"))
	mustWriteFile(t, filepath.Join(root, "node_modules", "x.js"))
	mustWriteFile(t, filepath.Join(root, "Demo.app", "Contents", "Info.plist"))
	mustWriteFile(t, filepath.Join(root, "temp.tmp"))
	mustWriteFile(t, filepath.Join(root, ".DS_Store"))

	opts := launcherIndexOptions{
		roots:      []launcherIndexRoot{{path: root, maxDepth: 2, rank: 0}},
		maxEntries: 100,
		maxPerRoot: 100,
	}
	entries, stats := buildLauncherFileIndex(opts)

	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.name] = true
	}
	for _, want := range []string{"a.txt", "sub", "b.txt", "deep"} {
		if !names[want] {
			t.Errorf("索引应包含 %q，实际：%v", want, names)
		}
	}
	for _, skip := range []string{"secret.txt", "x.js", "Info.plist", "temp.tmp", ".DS_Store", "c.txt"} {
		if names[skip] {
			t.Errorf("索引不应包含 %q", skip)
		}
	}
	if stats.truncated {
		t.Error("未达到上限时不应标记截断")
	}
	if stats.errors != 0 {
		t.Errorf("临时目录不应有读取错误，实际 %d", stats.errors)
	}
}

func TestBuildLauncherFileIndexTruncates(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		mustWriteFile(t, filepath.Join(root, "file"+string(rune('a'+i))+".txt"))
	}
	opts := launcherIndexOptions{
		roots:      []launcherIndexRoot{{path: root, maxDepth: 2, rank: 0}},
		maxEntries: 3,
		maxPerRoot: 3,
	}
	entries, stats := buildLauncherFileIndex(opts)
	if len(entries) != 3 {
		t.Fatalf("应截断到 3 条，实际 %d", len(entries))
	}
	if !stats.truncated {
		t.Error("达到上限后应标记截断")
	}
}

func TestBuildLauncherFileIndexSkipsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	mustWriteFile(t, target)
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 创建符号链接需要额外权限")
		}
		t.Fatal(err)
	}

	opts := launcherIndexOptions{
		roots:      []launcherIndexRoot{{path: root, maxDepth: 2, rank: 0}},
		maxEntries: 100,
		maxPerRoot: 100,
	}
	entries, _ := buildLauncherFileIndex(opts)
	for _, e := range entries {
		if e.name == "link.txt" {
			t.Fatal("符号链接不应被收录")
		}
	}
}

func TestSkipLauncherName(t *testing.T) {
	cases := []struct {
		name  string
		isDir bool
		want  bool
	}{
		{".hidden", true, true},
		{"node_modules", true, true},
		{"Demo.app", true, true},
		{"Library", true, true},
		{"src", true, false},
		{"temp.tmp", false, true},
		{".DS_Store", false, true},
		{"report.pdf", false, false},
	}
	for _, c := range cases {
		if got := skipLauncherName(c.name, c.isDir); got != c.want {
			t.Errorf("skipLauncherName(%q, %v) = %v，期望 %v", c.name, c.isDir, got, c.want)
		}
	}
}
