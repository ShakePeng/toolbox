package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// base64Decode 便于往返校验，与 powerShellEncodedCommand 的编码互逆。
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// osascript 与 PowerShell 都是每行一个路径，行尾 \n 或 \r\n。
func TestParseFileManagerSelectionLines(t *testing.T) {
	if got := parseFileManagerSelectionLines(""); got != nil {
		t.Errorf("空输出应返回 nil，实际 %v", got)
	}
	if got := parseFileManagerSelectionLines("\r\n\n  \n"); got != nil {
		t.Errorf("全空白输出应返回 nil，实际 %v", got)
	}
	got := parseFileManagerSelectionLines("/tmp/a.png\r\n/Users/t/图片 2.jpg\n\n/tmp/b.webp\r\n")
	want := []string{"/tmp/a.png", "/Users/t/图片 2.jpg", "/tmp/b.webp"}
	if len(got) != len(want) {
		t.Fatalf("应解析出 %d 条路径，实际 %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条应为 %q，实际 %q", i, want[i], got[i])
		}
	}
}

// -EncodedCommand 是 UTF-16LE 字节流的 base64，往返解码应与原文一致，
// 覆盖中文脚本文本（Windows 侧纯逻辑在 macOS 上也能测）。
func TestPowerShellEncodedCommand(t *testing.T) {
	script := "[Console]::OutputEncoding = UTF8\nWrite-Output 你好"
	enc := powerShellEncodedCommand(script)
	raw, err := base64Decode(enc)
	if err != nil {
		t.Fatalf("base64 解码失败：%v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("UTF-16LE 字节数应为偶数，实际 %d", len(raw))
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		// 小端：低字节在前。
		units[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Errorf("解码往返不一致：%q", got)
	}
}

// Finder/资源管理器选中的图片入队等待，非图片被拒并提示；不自动压缩。
func TestEnqueueFileManagerSelectionAddsQueuedImages(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "a.png")
	if err := os.WriteFile(pngPath, testImageBytes(t, imageFormatPNG, 24, 24), 0o644); err != nil {
		t.Fatal(err)
	}
	jpgPath := filepath.Join(dir, "b.jpg")
	if err := os.WriteFile(jpgPath, testImageBytes(t, imageFormatJPEG, 24, 24), 0o644); err != nil {
		t.Fatal(err)
	}
	txtPath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txtPath, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "folder")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	s := &imageCompressSession{app: a}
	a.compressor = s
	defer func() { fetchFileManagerSelection = fileManagerSelection }()
	fetchFileManagerSelection = func(uintptr) ([]string, error) {
		return []string{pngPath, jpgPath, txtPath, subDir}, nil
	}

	a.enqueueFileManagerSelection(0)

	if len(s.items) != 2 {
		t.Fatalf("应入队 2 张图片，实际 %d", len(s.items))
	}
	for i, want := range []string{pngPath, jpgPath} {
		if s.items[i].SourcePath != want {
			t.Errorf("第 %d 项应为 %q，实际 %q", i, want, s.items[i].SourcePath)
		}
		if s.items[i].Status != tinyPending {
			t.Errorf("入队后应处于待压缩状态，实际 %q", s.items[i].Status)
		}
	}
	if s.running {
		t.Error("带入选中文件不应自动开始压缩")
	}
	if !strings.Contains(s.notice, "跳过 2") {
		t.Errorf("notice 应提示跳过 2 个非图片，实际 %q", s.notice)
	}
}

// 同一批选区重复带入时走 addPaths 去重；空选区静默返回，不写 notice。
func TestEnqueueFileManagerSelectionDedupAndEmpty(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "a.png")
	if err := os.WriteFile(pngPath, testImageBytes(t, imageFormatPNG, 24, 24), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	s := &imageCompressSession{app: a}
	a.compressor = s
	defer func() { fetchFileManagerSelection = fileManagerSelection }()
	fetchFileManagerSelection = func(uintptr) ([]string, error) {
		return []string{pngPath}, nil
	}

	a.enqueueFileManagerSelection(0)
	s.notice = ""
	a.enqueueFileManagerSelection(0)

	if len(s.items) != 1 {
		t.Fatalf("重复带入同一文件应去重，实际 %d 项", len(s.items))
	}
	if s.notice != "" {
		t.Errorf("纯去重不应再写 notice，实际 %q", s.notice)
	}

	fetchFileManagerSelection = func(uintptr) ([]string, error) { return nil, nil }
	a.enqueueFileManagerSelection(0)
	if len(s.items) != 1 || s.notice != "" {
		t.Errorf("空选区应静默退化，实际 %d 项 / notice %q", len(s.items), s.notice)
	}
}

// 读取失败（如自动化授权被拒）时只在窗口内提示，队列保持不动。
func TestEnqueueFileManagerSelectionErrorNoticed(t *testing.T) {
	a := newTestApp()
	s := &imageCompressSession{app: a}
	a.compressor = s
	defer func() { fetchFileManagerSelection = fileManagerSelection }()
	fetchFileManagerSelection = func(uintptr) ([]string, error) {
		return nil, os.ErrPermission
	}

	a.enqueueFileManagerSelection(0)

	if len(s.items) != 0 {
		t.Errorf("失败时不应入队，实际 %d 项", len(s.items))
	}
	if !strings.Contains(s.notice, "读取文件管理器选区失败") {
		t.Errorf("notice 应包含失败前缀，实际 %q", s.notice)
	}
}
