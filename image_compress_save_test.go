package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveTinyBesideUsesUniqueNames(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.PNG")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := saveTinyBeside(source, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := saveTinyBeside(source, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "photo-tiny.PNG" || filepath.Base(second) != "photo-tiny-2.PNG" {
		t.Fatalf("输出重名处理错误：%s, %s", first, second)
	}
	if data, _ := os.ReadFile(source); string(data) != "original" {
		t.Fatal("另存不能修改原图")
	}
	if data, _ := os.ReadFile(second); string(data) != "two" {
		t.Fatal("第二份输出内容错误")
	}
}

func TestOverwriteTinyOriginal(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "photo.png")
	original := testImageBytes(t, imageFormatPNG, 20, 10)
	compressed := testImageBytes(t, imageFormatPNG, 8, 6)
	if err := os.WriteFile(source, original, 0640); err != nil {
		t.Fatal(err)
	}
	// Windows 没有 POSIX 权限位，WriteFile(0640) 落盘就是 0666、Chmod 也只认只读位，
	// 所以这里比对覆盖前后的实际权限，而不是写死 0640。
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := overwriteTinyOriginal(source, imageFormatPNG, compressed); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(got, compressed) {
		t.Fatalf("原图没有被替换：%v", err)
	}
	info, _ := os.Stat(source)
	if info.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("覆盖后应保留权限，覆盖前 %v，实际 %v", before.Mode().Perm(), info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("成功后不应残留临时文件：%v", entries)
	}
}

func TestOverwriteTinyOriginalRejectsBadResult(t *testing.T) {
	source := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := overwriteTinyOriginal(source, imageFormatPNG, []byte("bad")); err == nil {
		t.Fatal("无效压缩结果不应覆盖")
	}
	if got, _ := os.ReadFile(source); string(got) != "original" {
		t.Fatal("校验失败时原图被修改")
	}
}

func TestOverwriteTinyOriginalRollback(t *testing.T) {
	source := filepath.Join(t.TempDir(), "photo.png")
	original := testImageBytes(t, imageFormatPNG, 12, 12)
	compressed := testImageBytes(t, imageFormatPNG, 6, 6)
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	oldRename := tinyRename
	defer func() { tinyRename = oldRename }()
	calls := 0
	tinyRename = func(oldpath, newpath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected replace failure")
		}
		return oldRename(oldpath, newpath)
	}
	if err := overwriteTinyOriginal(source, imageFormatPNG, compressed); err == nil || !strings.Contains(err.Error(), "已恢复原图") {
		t.Fatalf("应报告替换失败并恢复：%v", err)
	}
	got, _ := os.ReadFile(source)
	if !bytes.Equal(got, original) {
		t.Fatal("替换失败后原图内容没有恢复")
	}
	entries, _ := os.ReadDir(filepath.Dir(source))
	if len(entries) != 1 {
		t.Fatalf("回滚后不应残留备份：%v", entries)
	}
}

func TestWriteTinyZIPAndUniqueNames(t *testing.T) {
	output := filepath.Join(t.TempDir(), "images.zip")
	items := []tinyZIPItem{
		{Name: "one/a.png", Data: []byte("one")},
		{Name: "two/a.png", Data: []byte("two")},
		{Name: "..\\b.png", Data: []byte("bee")},
	}
	if err := writeTinyZIP(output, items); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 3 || zr.File[0].Name != "a.png" || zr.File[1].Name != "a-2.png" || zr.File[2].Name != "b.png" {
		names := []string{}
		for _, file := range zr.File {
			names = append(names, file.Name)
		}
		t.Fatalf("ZIP 名称不安全或重复：%v", names)
	}
}

func TestWriteTinyZIPRequiresItems(t *testing.T) {
	if err := writeTinyZIP(filepath.Join(t.TempDir(), "empty.zip"), nil); err == nil {
		t.Fatal("空选择不应生成 ZIP")
	}
}
