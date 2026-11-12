package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// macOS 的 NSFileManager.displayNameAtPath 对缺少 CFBundleDevelopmentRegion
// 的 bundle 会回落英文名（如「同程管家」显示成 TIEXIN），中文名只写在
// Resources/zh-Hans.lproj/InfoPlist.strings 里。这里直接读这些本地化
// 字符串文件补上，Spotlight 显示的名字也是从同一处取的。
var appLocalizations = []string{"zh-Hans", "zh_CN", "zh-Hant", "zh_TW"}

// localizedAppName 返回 .app 的中文显示名，取不到时返回空串。
// 只在后台枚举应用时调用，不在渲染路径执行。
func localizedAppName(path string) string {
	if path == "" || !strings.HasSuffix(strings.ToLower(path), ".app") {
		return ""
	}
	resources := filepath.Join(path, "Contents", "Resources")
	for _, lang := range appLocalizations {
		data, err := os.ReadFile(filepath.Join(resources, lang+".lproj", "InfoPlist.strings"))
		if err != nil {
			continue
		}
		if name := plistLocalizedString(data, "CFBundleDisplayName"); name != "" {
			return name
		}
		if name := plistLocalizedString(data, "CFBundleName"); name != "" {
			return name
		}
	}
	return ""
}

// plistLocalizedString 从 InfoPlist.strings 里取一个字符串值。同时兼容
// 文本格式（"Key" = "值";）和 XML 格式；二进制 plist 不手写解析，留给
// 系统原名兜底，避免解析出错时把应用名改坏。
func plistLocalizedString(data []byte, key string) string {
	text := decodePlistText(data)
	if text == "" || strings.HasPrefix(text, "bplist") {
		return ""
	}
	quoted := regexp.QuoteMeta(key)
	patterns := []string{
		`(?m)"?` + quoted + `"?\s*=\s*"((?:[^"\\]|\\.)*)"\s*;`,
		`(?ms)<key>\s*` + quoted + `\s*</key>\s*<string>([^<]*)</string>`,
	}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			if v := strings.TrimSpace(unescapePlistString(m[1])); v != "" {
				return v
			}
		}
	}
	return ""
}

// decodePlistText 把 strings 文件转成 UTF-8。macOS 上不少应用（如微信）的
// InfoPlist.strings 是带 BOM 的 UTF-16，按 UTF-8 解析会读不到中文名。
func decodePlistText(data []byte) string {
	if len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe || data[0] == 0xfe && data[1] == 0xff) {
		decoder := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()
		if out, _, err := transform.Bytes(decoder, data); err == nil {
			return string(out)
		}
	}
	return string(data)
}

// unescapePlistString 处理字符串值里常见的转义，取消转义不会引入新的转义序列，
// 所以用一次性 Replacer 就够。
func unescapePlistString(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(s)
}
