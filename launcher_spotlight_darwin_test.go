//go:build darwin

package main

import "testing"

func TestParseSpotlightNames(t *testing.T) {
	out := "kMDItemDisplayName = \"终端\"\n" +
		"kMDItemDisplayName = (null)\n" +
		"kMDItemDisplayName = \"Safari浏览器\"\n"
	paths := []string{"/A.app", "/B.app", "/C.app"}

	got := parseSpotlightNames(out, paths)
	if got["/A.app"] != "终端" || got["/C.app"] != "Safari浏览器" {
		t.Errorf("应解析出中文名，实际 %+v", got)
	}
	// 中间的文件属性缺失时是 (null)，不能把后一个名字错位到它身上。
	if _, ok := got["/B.app"]; ok {
		t.Errorf("(null) 不应映射出名字：%+v", got)
	}
}
