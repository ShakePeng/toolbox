package main

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
)

// 启动器结果类型。应用和文件混排展示，但排序时应用优先。
type launcherKind uint8

const (
	launcherKindApp launcherKind = iota
	launcherKindFile
	launcherKindDir
	launcherKindProject
	// launcherKindAction 是命令触发的操作行（如重建索引），不对应文件或应用。
	launcherKindAction
)

// launcherAction 是操作行要执行的动作。
type launcherAction uint8

const (
	launcherActionNone launcherAction = iota
	launcherActionReindex
	// launcherActionProject 进入某个项目源的项目模式。
	launcherActionProject
)

// launcherItem 是浮窗列表里的一行。
type launcherItem struct {
	kind     launcherKind
	title    string
	subtitle string
	path     string
	app      mygo.InstalledApp
	score    int
	action   launcherAction
	// project 是项目提示行对应的源 id（vscode / fork）。
	project string
}

func (k launcherKind) label() string {
	switch k {
	case launcherKindApp:
		return "应用"
	case launcherKindDir:
		return "文件夹"
	case launcherKindProject:
		return "项目"
	case launcherKindAction:
		return "操作"
	default:
		return "文件"
	}
}

// launcherAppEntry 缓存应用的小写字段，避免每次按键重复转换。
type launcherAppEntry struct {
	app       mygo.InstalledApp
	nameLower string
	pathLower string
	altLower  string
}

// launcherFileEntry 是文件索引中的一项。
type launcherFileEntry struct {
	path      string
	name      string
	dir       string
	nameLower string
	pathLower string
	isDir     bool
	// depth 是从索引根算起的层级，rank 是根目录优先级；两者只用于排序。
	depth int
	rank  int
}

type launcherSources struct {
	apps         []launcherAppEntry
	files        []launcherFileEntry
	includeApps  bool
	includeFiles bool
}

// launcherFilter 过滤并排序结果。空查询不展示任何结果：此时浮窗只保留
// 搜索框，用户输入后再展开结果区域。
func launcherFilter(query string, src launcherSources, limit int) []launcherItem {
	if limit <= 0 {
		return nil
	}
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return nil
	}

	items := make([]launcherItem, 0, limit*2)
	if src.includeApps {
		for _, a := range src.apps {
			score := launcherScoreTerms(terms, a.nameLower, a.pathLower+" "+a.altLower)
			if score < 0 {
				continue
			}
			items = append(items, launcherItem{
				kind:     launcherKindApp,
				title:    a.app.Name,
				subtitle: launcherAppSubtitle(a.app),
				path:     a.app.Path,
				app:      a.app,
				score:    score,
			})
		}
	}
	if src.includeFiles {
		for _, f := range src.files {
			score := launcherScoreTerms(terms, f.nameLower, f.pathLower)
			if score < 0 {
				continue
			}
			// 浅层文件更可能是用户想找的，同分时优先。
			score -= min(f.depth*4, 40)
			kind := launcherKindFile
			if f.isDir {
				kind = launcherKindDir
			}
			items = append(items, launcherItem{
				kind:     kind,
				title:    f.name,
				subtitle: f.dir,
				path:     f.path,
				score:    score,
			})
		}
	}

	items = dedupeLauncherItems(items)
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if al, bl := strings.ToLower(a.title), strings.ToLower(b.title); al != bl {
			return al < bl
		}
		return a.path < b.path
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// launcherScoreTerms 要求每个词都命中，分数求和；任一未命中返回 -1。
func launcherScoreTerms(terms []string, primary, secondary string) int {
	total := 0
	for _, term := range terms {
		score := launcherScore(term, primary, secondary)
		if score < 0 {
			return -1
		}
		total += score
	}
	return total
}

// launcherScore 给单个词打分。主字段（名称）优先于辅助字段（路径）。
func launcherScore(term, primary, secondary string) int {
	if term == "" {
		return 0
	}
	if score := launcherFieldScore(term, primary); score >= 0 {
		return score
	}
	if score := launcherFieldScore(term, secondary); score >= 0 {
		// 命中路径/目录的优先级低于名称命中。
		return score - 150
	}
	return -1
}

// launcherFieldScore 的 term/field 都应已小写化，避免每次按键对上千条
// 路径重复分配小写副本。大小写归一化在 launcherFilter 入口统一完成。
func launcherFieldScore(term, field string) int {
	if field == "" {
		return -1
	}
	field = strings.ReplaceAll(field, "\\", "/")
	if field == term {
		return 1000
	}
	pos := strings.Index(field, term)
	if pos < 0 {
		return -1
	}
	penalty := min(pos, 50)
	if pos == 0 {
		return 800 - penalty
	}
	if launcherWordBoundary(field, pos) {
		return 600 - penalty
	}
	return 400 - penalty
}

// launcherWordBoundary 判断 pos 是否是一个词的起点，覆盖
// “Visual Studio Code”“my_document”“foo-bar” 这类命名。
func launcherWordBoundary(s string, pos int) bool {
	if pos <= 0 {
		return true
	}
	switch s[pos-1] {
	case ' ', '.', '_', '-', '/', '\\', '(', '[', '{':
		return true
	}
	return false
}

func dedupeLauncherItems(items []launcherItem) []launcherItem {
	if len(items) < 2 {
		return items
	}
	best := make(map[string]int, len(items))
	out := items[:0]
	for _, it := range items {
		key := strings.ToLower(strings.ReplaceAll(it.path, "\\", "/"))
		if key == "" {
			key = strings.ToLower(it.title)
		}
		if idx, ok := best[key]; ok {
			if it.score > out[idx].score {
				out[idx] = it
			}
			continue
		}
		best[key] = len(out)
		out = append(out, it)
	}
	return out
}

func clampLauncherSelection(cur, n, delta int) int {
	if n <= 0 {
		return 0
	}
	next := cur + delta
	if next < 0 {
		return 0
	}
	if next >= n {
		return n - 1
	}
	return next
}

// launcherAppSubtitle 给结果行提供可读的副标题。Windows 的 shell 路径前缀对用户没有意义。
func launcherAppSubtitle(app mygo.InstalledApp) string {
	p := app.Path
	const shellPrefix = `shell:AppsFolder\`
	if strings.HasPrefix(p, shellPrefix) {
		return strings.TrimPrefix(p, shellPrefix)
	}
	if dir := filepath.Dir(p); dir != "." && dir != string(filepath.Separator) {
		return dir
	}
	return p
}
