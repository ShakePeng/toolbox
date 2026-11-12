package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const launcherMaxProjects = 2000

// launcherProjectEntry 是项目文件夹根目录下的一个直接子目录。
type launcherProjectEntry struct {
	path      string
	name      string
	root      string
	nameLower string
	pathLower string
}

// launcherProjectSource 描述一个「命令 + 空格」项目模式。
// gitOnly 用于 Fork：只能打开 Git 仓库，列表里只保留含 .git 的目录。
type launcherProjectSource struct {
	id      string
	label   string
	prefix  string
	folders []string
	gitOnly bool
}

// projectSources 按启用的工具返回项目源，顺序即输入匹配的优先级。
func (a *app) projectSources() []launcherProjectSource {
	if a == nil {
		return nil
	}
	var out []launcherProjectSource
	if a.settings.LauncherVSCodeEnabled {
		out = append(out, launcherProjectSource{
			id: "vscode", label: "VS Code", prefix: a.vscodeCommand(),
			folders: a.settings.LauncherVSCodeFolders,
		})
	}
	if a.settings.LauncherForkEnabled {
		out = append(out, launcherProjectSource{
			id: "fork", label: "Fork", prefix: a.forkCommand(),
			folders: a.settings.LauncherForkFolders, gitOnly: true,
		})
	}
	if a.settings.LauncherTraeEnabled {
		out = append(out, launcherProjectSource{
			id: "trae", label: "Trae", prefix: a.traeCommand(),
			folders: a.settings.LauncherTraeFolders,
		})
	}
	return out
}

// projectQuery 判断当前输入是否是某个源的「命令 + 空格」项目模式，
// 并返回该源与空格后的查询词。这里不能先 TrimSpace，否则命令后的
// 空格会被吃掉。
func (s *launcherSession) projectQuery() (launcherProjectSource, string, bool) {
	if s == nil || s.app == nil {
		return launcherProjectSource{}, "", false
	}
	raw := strings.TrimLeft(s.query, " \t")
	sep := strings.IndexAny(raw, " \t")
	if sep <= 0 {
		s.projectSource = ""
		return launcherProjectSource{}, "", false
	}
	command := raw[:sep]
	// 单个字母的前缀太容易和普通多词搜索混淆，至少两个字符才认作命令。
	if utf8.RuneCountInString(command) < 2 {
		s.projectSource = ""
		return launcherProjectSource{}, "", false
	}
	query := strings.TrimSpace(raw[sep+1:])
	for _, src := range s.app.projectSources() {
		prefix := strings.TrimSpace(src.prefix)
		if prefix == "" {
			continue
		}
		// 命令部分可以只输入触发词的前缀，少打几个字符也能进项目模式。
		if !strings.HasPrefix(strings.ToLower(prefix), strings.ToLower(command)) {
			continue
		}
		s.projectSource = src.id
		return src, query, true
	}
	s.projectSource = ""
	return launcherProjectSource{}, "", false
}

// projectPrefix 返回某个项目源配置的触发词，找不到时返回空串。
func (a *app) projectPrefix(id string) string {
	if a == nil {
		return ""
	}
	for _, src := range a.projectSources() {
		if src.id == id {
			return strings.TrimSpace(src.prefix)
		}
	}
	return ""
}

// currentProjectSource 返回当前项目模式对应的源；不在项目模式时返回 nil。
func (s *launcherSession) currentProjectSource() (launcherProjectSource, bool) {
	if s == nil || s.app == nil || s.projectSource == "" {
		return launcherProjectSource{}, false
	}
	for _, src := range s.app.projectSources() {
		if src.id == s.projectSource {
			return src, true
		}
	}
	return launcherProjectSource{}, false
}

// normalizeProjectFolders 展开 ~、转成绝对路径并去重。
func normalizeProjectFolders(folders []string) []string {
	out := make([]string, 0, len(folders))
	seen := make(map[string]struct{}, len(folders))
	for _, folder := range folders {
		folder = strings.TrimSpace(folder)
		if folder == "" {
			continue
		}
		folder = expandUserFolder(folder)
		if !filepath.IsAbs(folder) {
			if abs, err := filepath.Abs(folder); err == nil {
				folder = abs
			}
		}
		folder = filepath.Clean(folder)
		key := strings.ToLower(folder)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, folder)
	}
	return out
}

func expandUserFolder(folder string) string {
	if folder != "~" && !strings.HasPrefix(folder, "~/") && !strings.HasPrefix(folder, `~\`) {
		return folder
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return folder
	}
	if folder == "~" {
		return home
	}
	return filepath.Join(home, folder[2:])
}

// scanProjectFolders 只扫描每个根目录的下一级子目录，不递归。项目通常就是
// 根目录下的一层文件夹；递归会把 node_modules 之类的噪音带进来。
// gitOnly 为 true 时只保留含 .git 的目录（Fork 只能打开仓库）。
func scanProjectFolders(roots []string, limit int, gitOnly bool) ([]launcherProjectEntry, []string) {
	if limit <= 0 {
		limit = launcherMaxProjects
	}
	var out []launcherProjectEntry
	var errs []string
	seen := make(map[string]struct{})

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			errs = append(errs, root+" 无法读取")
			continue
		}
		for _, ent := range entries {
			if len(out) >= limit {
				return out, errs
			}
			name := ent.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if ent.Type()&os.ModeSymlink != 0 {
				continue
			}
			isDir := ent.IsDir()
			if !isDir {
				if info, err := ent.Info(); err == nil {
					isDir = info.IsDir()
				}
			}
			if !isDir {
				continue
			}
			path := filepath.Join(root, name)
			if gitOnly && !isGitRepository(path) {
				continue
			}
			key := launcherPathKey(path)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, launcherProjectEntry{
				path:      path,
				name:      name,
				root:      root,
				nameLower: strings.ToLower(name),
				pathLower: strings.ToLower(strings.ReplaceAll(path, "\\", "/")),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].nameLower != out[j].nameLower {
			return out[i].nameLower < out[j].nameLower
		}
		return out[i].path < out[j].path
	})
	return out, errs
}

// isGitRepository 判断目录是否是 Git 仓库。worktree 和子模块的 .git 是文件，
// 所以只检查是否存在，不要求是目录。
func isGitRepository(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func filterLauncherProjects(query string, projects []launcherProjectEntry, limit int) []launcherItem {
	if limit <= 0 {
		return nil
	}
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		items := make([]launcherItem, 0, min(limit, len(projects)))
		for _, p := range projects {
			items = append(items, launcherProjectItem(p, 0))
			if len(items) >= limit {
				break
			}
		}
		return items
	}

	items := make([]launcherItem, 0, limit*2)
	for _, p := range projects {
		score := launcherScoreTerms(terms, p.nameLower, p.pathLower)
		if score < 0 {
			continue
		}
		items = append(items, launcherProjectItem(p, score))
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.score != b.score {
			return a.score > b.score
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

func launcherProjectItem(p launcherProjectEntry, score int) launcherItem {
	return launcherItem{
		kind:     launcherKindProject,
		title:    p.name,
		subtitle: p.root,
		path:     p.path,
		score:    score,
	}
}

// ensureProjects 在进入项目模式时后台扫描。源、根目录列表变化或重新唤起时
// 会重新扫描，旧结果通过 projectGen 丢弃。
func (s *launcherSession) ensureProjects(src launcherProjectSource) {
	if s == nil || s.app == nil {
		return
	}
	roots := normalizeProjectFolders(src.folders)
	// 签名带上源 id：VSCode 和 Fork 的项目缓存不能互相复用。
	sig := src.id + "\x00" + strings.Join(roots, "\x00")
	if s.projectsLoaded && s.projectRootsSig == sig {
		return
	}
	// 扫描进行中且根目录没变时不要每个按键都重新扫一遍。
	if s.projectsLoading && s.projectRootsSig == sig {
		return
	}
	if len(roots) == 0 {
		s.projects = nil
		s.projectsLoaded = true
		s.projectsLoading = false
		s.projectErrs = nil
		s.projectRootsSig = sig
		return
	}
	if s.win == nil || s.closed {
		return
	}

	s.projectGen++
	gen := s.projectGen
	s.projectsLoading = true
	s.projectRootsSig = sig
	go func() {
		projects, errs := scanProjectFolders(roots, launcherMaxProjects, src.gitOnly)
		s.win.Update(func() {
			if s.closed || s.app.quitting || s.projectGen != gen {
				return
			}
			s.projects = projects
			s.projectErrs = errs
			s.projectsLoading = false
			s.projectsLoaded = true
			s.rebuildResults()
		})
	}()
}

// projectSettingsChanged 在命令或文件夹配置变化后清空项目缓存并重扫。
func (s *launcherSession) projectSettingsChanged() {
	if s == nil {
		return
	}
	s.projectGen++
	s.projects = nil
	s.projectsLoaded = false
	s.projectsLoading = false
	s.projectErrs = nil
	s.projectRootsSig = ""
	s.rebuildResults()
	s.refresh()
}

// vscodeSettingsChanged 保留给 VSCode 配置的调用点，语义与通用方法一致。
func (s *launcherSession) vscodeSettingsChanged() { s.projectSettingsChanged() }

// vscodeChanged 只表示命令探测结果变了，项目列表可以继续复用。
func (s *launcherSession) vscodeChanged() {
	if s == nil {
		return
	}
	s.rebuildResults()
	s.refresh()
}

func (s *launcherSession) projectRoots() []string {
	src, ok := s.currentProjectSource()
	if !ok {
		return nil
	}
	return normalizeProjectFolders(src.folders)
}

func (s *launcherSession) projectStatusLine() string {
	src, ok := s.currentProjectSource()
	if !ok || s.app == nil {
		return ""
	}
	roots := normalizeProjectFolders(src.folders)
	switch {
	case len(roots) == 0:
		return "未配置项目文件夹"
	case s.projectsLoading:
		return src.label + " · 正在扫描项目"
	}
	parts := []string{fmt.Sprintf("%s · %d 个项目", src.label, len(s.projects))}
	switch src.id {
	case "fork":
		switch {
		case s.app.fork.checking:
			parts = append(parts, "检测命令中")
		case s.app.forkIsReady():
			parts = append(parts, "命令已就绪")
		case s.app.fork.err != "":
			parts = append(parts, "命令不可用")
		}
	case "trae":
		switch {
		case s.app.trae.checking:
			parts = append(parts, "检测命令中")
		case s.app.traeIsReady():
			parts = append(parts, "命令已就绪")
		case s.app.trae.err != "":
			parts = append(parts, "命令不可用")
		}
	default:
		switch {
		case s.app.vscode.checking:
			parts = append(parts, "检测命令中")
		case s.app.vscodeIsReady():
			parts = append(parts, "命令已就绪")
		case s.app.vscode.err != "":
			parts = append(parts, "命令不可用")
		}
	}
	return strings.Join(parts, " · ")
}
