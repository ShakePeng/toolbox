package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// projectQuery 判断当前输入是否是“命令 + 空格”的项目模式，并返回空格后的
// 查询词。这里不能先 TrimSpace，否则 code 后面的空格会被吃掉。
func (s *launcherSession) projectQuery() (string, bool) {
	if s == nil || s.app == nil || !s.app.settings.LauncherVSCodeEnabled {
		return "", false
	}
	command := strings.TrimSpace(s.app.settings.LauncherVSCodeCommand)
	if command == "" {
		command = "code"
	}
	raw := strings.TrimLeft(s.query, " \t")
	if len(raw) <= len(command) {
		return "", false
	}
	if !strings.EqualFold(raw[:len(command)], command) {
		return "", false
	}
	if raw[len(command)] != ' ' && raw[len(command)] != '\t' {
		return "", false
	}
	return strings.TrimSpace(raw[len(command)+1:]), true
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
func scanProjectFolders(roots []string, limit int) ([]launcherProjectEntry, []string) {
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

// ensureProjects 在进入项目模式时后台扫描。根目录列表变化或重新唤起时
// 会重新扫描，旧结果通过 projectGen 丢弃。
func (s *launcherSession) ensureProjects() {
	if s == nil || s.app == nil || !s.app.settings.LauncherVSCodeEnabled {
		return
	}
	roots := normalizeProjectFolders(s.app.settings.LauncherVSCodeFolders)
	sig := strings.Join(roots, "\x00")
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
		projects, errs := scanProjectFolders(roots, launcherMaxProjects)
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

// vscodeSettingsChanged 在命令或文件夹配置变化后清空项目缓存并重扫。
func (s *launcherSession) vscodeSettingsChanged() {
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

// vscodeChanged 只表示命令探测结果变了，项目列表可以继续复用。
func (s *launcherSession) vscodeChanged() {
	if s == nil {
		return
	}
	s.rebuildResults()
	s.refresh()
}

func (s *launcherSession) projectRoots() []string {
	if s == nil || s.app == nil {
		return nil
	}
	return normalizeProjectFolders(s.app.settings.LauncherVSCodeFolders)
}

func (s *launcherSession) projectStatusLine() string {
	roots := s.projectRoots()
	command := "code"
	if s.app != nil {
		command = s.app.vscodeCommand()
	}
	switch {
	case len(roots) == 0:
		return "未配置项目文件夹"
	case s.projectsLoading:
		return command + " · 正在扫描项目"
	}
	parts := []string{fmt.Sprintf("%s · %d 个项目", command, len(s.projects))}
	if s.app != nil {
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
