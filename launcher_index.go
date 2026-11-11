package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
)

// 文件索引的规模上限。常用目录里几千条已经足够启动器使用，
// 超过后继续遍历只会拖慢首次唤起。
const (
	launcherMaxIndexEntries        = 5000
	launcherMaxIndexEntriesPerRoot = 2500
)

type launcherIndexRoot struct {
	path     string
	maxDepth int
	rank     int
}

type launcherIndexOptions struct {
	roots      []launcherIndexRoot
	maxEntries int
	maxPerRoot int
}

type launcherIndexStats struct {
	files     int
	dirs      int
	skipped   int
	errors    int
	truncated bool
}

// launcherFileIndex 是会话持有的索引状态。
type launcherFileIndex struct {
	entries  []launcherFileEntry
	stats    launcherIndexStats
	built    bool
	building bool
}

// defaultLauncherIndexOptions 把常用目录变成索引根。深度按“根的直接子项为第 1 层”
// 计算；主目录只扫浅层，避免把 Library/AppData 等整棵树拖进来。
func defaultLauncherIndexOptions() launcherIndexOptions {
	var roots []launcherIndexRoot
	add := func(name mygo.PathName, maxDepth, rank int) {
		p, err := mygo.App.Path(name)
		if err != nil || strings.TrimSpace(p) == "" {
			return
		}
		roots = append(roots, launcherIndexRoot{path: p, maxDepth: maxDepth, rank: rank})
	}
	add(mygo.PathDesktop, 4, 0)
	add(mygo.PathDownloads, 4, 1)
	add(mygo.PathDocuments, 4, 2)
	add(mygo.PathHome, 1, 3)
	return launcherIndexOptions{
		roots:      roots,
		maxEntries: launcherMaxIndexEntries,
		maxPerRoot: launcherMaxIndexEntriesPerRoot,
	}
}

type launcherWalkState struct {
	maxEntries int
	maxPerRoot int
	perRoot    int
	total      int
	stop       bool
	seen       map[string]struct{}
	entries    []launcherFileEntry
	stats      launcherIndexStats
}

// buildLauncherFileIndex 遍历常用目录。它是纯文件系统逻辑，测试可以直接传临时目录作为根。
func buildLauncherFileIndex(opts launcherIndexOptions) ([]launcherFileEntry, launcherIndexStats) {
	if opts.maxEntries <= 0 {
		opts.maxEntries = launcherMaxIndexEntries
	}
	if opts.maxPerRoot <= 0 {
		opts.maxPerRoot = launcherMaxIndexEntriesPerRoot
	}

	st := &launcherWalkState{
		maxEntries: opts.maxEntries,
		maxPerRoot: opts.maxPerRoot,
		seen:       make(map[string]struct{}),
	}
	seenRoots := make(map[string]struct{}, len(opts.roots))
	for _, root := range opts.roots {
		if st.stop {
			break
		}
		if strings.TrimSpace(root.path) == "" {
			continue
		}
		key := launcherPathKey(root.path)
		if _, ok := seenRoots[key]; ok {
			continue
		}
		seenRoots[key] = struct{}{}
		if info, err := os.Stat(root.path); err != nil || !info.IsDir() {
			st.stats.errors++
			continue
		}
		st.perRoot = 0
		walkLauncherDir(root.path, 1, root.maxDepth, root.rank, st)
	}

	sort.SliceStable(st.entries, func(i, j int) bool {
		a, b := st.entries[i], st.entries[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		if a.nameLower != b.nameLower {
			return a.nameLower < b.nameLower
		}
		return a.path < b.path
	})
	return st.entries, st.stats
}

func walkLauncherDir(dir string, depth, maxDepth, rank int, st *launcherWalkState) {
	if st.stop || depth > maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		st.stats.errors++
		return
	}
	for _, ent := range entries {
		if st.stop {
			return
		}
		name := ent.Name()
		// 符号链接一律跳过：既不收录也不递归，避免跳出根目录或形成环。
		if ent.Type()&os.ModeSymlink != 0 {
			st.stats.skipped++
			continue
		}
		info, err := ent.Info()
		if err != nil {
			st.stats.errors++
			continue
		}
		isDir := info.IsDir()
		if skipLauncherName(name, isDir) {
			st.stats.skipped++
			continue
		}

		path := filepath.Join(dir, name)
		key := launcherPathKey(path)
		if _, ok := st.seen[key]; !ok {
			st.seen[key] = struct{}{}
			st.entries = append(st.entries, launcherFileEntry{
				path:      path,
				name:      name,
				dir:       dir,
				nameLower: strings.ToLower(name),
				pathLower: strings.ToLower(strings.ReplaceAll(path, "\\", "/")),
				isDir:     isDir,
				depth:     depth,
				rank:      rank,
			})
			st.total++
			st.perRoot++
			if isDir {
				st.stats.dirs++
			} else {
				st.stats.files++
			}
		}

		if st.total >= st.maxEntries || st.perRoot >= st.maxPerRoot {
			st.stats.truncated = true
			st.stop = true
			return
		}
		if isDir && depth < maxDepth {
			walkLauncherDir(path, depth+1, maxDepth, rank, st)
		}
	}
}

func launcherPathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

// launcherSkipDirNames 是目录名黑名单。构建产物和系统缓存既多又杂，
// 放进搜索结果只会淹没用户真正想找的文件。
var launcherSkipDirNames = map[string]struct{}{
	"library": {}, "appdata": {}, "application data": {}, "local settings": {},
	"$recycle.bin": {}, "system volume information": {}, "windows": {},
	"program files": {}, "program files (x86)": {}, "programdata": {},
	"node_modules": {}, "vendor": {}, "target": {}, "build": {}, "dist": {},
	"bin": {}, "obj": {}, "out": {}, ".git": {}, ".svn": {}, ".hg": {},
	".cache": {}, "cache": {}, "caches": {}, "tmp": {}, "temp": {}, "logs": {},
	"__pycache__": {}, ".venv": {}, "venv": {}, "env": {}, ".next": {},
	".nuxt": {}, ".gradle": {}, ".idea": {}, ".vscode": {}, ".npm": {},
	".pnpm-store": {}, ".cargo": {}, ".rustup": {},
}

// launcherSkipPackageSuffixes 是应用/资源包目录。应用由 InstalledApps 负责，
// 文件索引不再把整包拆开。
var launcherSkipPackageSuffixes = []string{
	".app", ".bundle", ".framework", ".plugin", ".xcodeproj",
	".xcworkspace", ".photoslibrary",
}

var launcherSkipFileExts = map[string]struct{}{
	".tmp": {}, ".temp": {}, ".swp": {}, ".part": {}, ".crdownload": {},
	".download": {}, ".lock": {}, ".log": {},
}

var launcherSkipFileNames = map[string]struct{}{
	".ds_store": {}, "desktop.ini": {}, "thumbs.db": {},
}

// skipLauncherName 过滤隐藏项、系统/构建目录和临时文件。
func skipLauncherName(name string, isDir bool) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || strings.HasPrefix(lower, ".") || strings.HasPrefix(lower, "~$") {
		return true
	}
	if isDir {
		if _, ok := launcherSkipDirNames[lower]; ok {
			return true
		}
		for _, suf := range launcherSkipPackageSuffixes {
			if strings.HasSuffix(lower, suf) {
				return true
			}
		}
		return false
	}
	if _, ok := launcherSkipFileNames[lower]; ok {
		return true
	}
	if _, ok := launcherSkipFileExts[strings.ToLower(filepath.Ext(lower))]; ok {
		return true
	}
	return false
}
