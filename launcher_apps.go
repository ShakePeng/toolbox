package main

import (
	"log"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	launcherIconSize        = 64
	launcherIconCacheMax    = 256
	launcherIconConcurrency = 3
)

type launcherIcon struct {
	bmp     *ui.Bitmap
	loading bool
	failed  bool
}

// ensureApps 在后台枚举已安装应用。InstalledApps 会读磁盘，文档要求从独立
// goroutine 调用，不能让首屏渲染等它。
func (s *launcherSession) ensureApps() {
	if s == nil || s.app == nil || !s.app.settings.LauncherSearchApps {
		return
	}
	if s.appsLoaded || s.appsLoading || s.win == nil || s.closed {
		return
	}
	s.appsLoading = true
	win := s.win
	gen := s.gen
	go func() {
		apps, err := mygo.Shell.InstalledApps()
		var entries []launcherAppEntry
		if err == nil {
			entries = buildLauncherAppEntries(apps)
		}
		win.Update(func() {
			if s.closed || s.app.quitting || s.gen != gen {
				return
			}
			s.appsLoading = false
			if err != nil {
				s.appsErr = err.Error()
				log.Printf("启动器枚举应用失败：%v", err)
				return
			}
			s.appsErr = ""
			s.appsLoaded = true
			s.apps = entries
			s.rebuildResults()
			s.app.refreshMain()
		})
	}()
}

// lookupSpotlightNames 允许测试注入，默认走 Spotlight（mdls）。
var lookupSpotlightNames = spotlightAppNames

func buildLauncherAppEntries(apps []mygo.InstalledApp) []launcherAppEntry {
	candidates := make([]mygo.InstalledApp, 0, len(apps))
	paths := make([]string, 0, len(apps))
	seen := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		if strings.TrimSpace(app.Path) == "" {
			continue
		}
		key := launcherPathKey(app.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, app)
		paths = append(paths, app.Path)
	}

	// Spotlight 的名字与 Finder/Launchpad 一致，系统自带应用的中文名只在这里；
	// 索引不可用时再读 bundle 内的本地化字符串，最后回退系统名。
	spot := lookupSpotlightNames(paths)
	entries := make([]launcherAppEntry, 0, len(candidates))
	for _, app := range candidates {
		name := strings.TrimSpace(spot[app.Path])
		if name == "" {
			name = localizedAppName(app.Path)
		}
		if name == "" {
			name = app.Name
		}
		aliases := []string{
			strings.TrimSuffix(filepath.Base(app.Path), filepath.Ext(app.Path)),
			app.ID,
		}
		// 显示名被本地化后，原名仍要能搜到（如「微信」与 WeChat）。
		if name != app.Name {
			aliases = append(aliases, app.Name)
			app.Name = name
		}
		entries = append(entries, launcherAppEntry{
			app:       app,
			nameLower: strings.ToLower(name),
			pathLower: strings.ToLower(strings.ReplaceAll(app.Path, "\\", "/")),
			altLower:  strings.ToLower(strings.TrimSpace(strings.Join(aliases, " "))),
		})
	}
	return entries
}

// ensureFileIndex 首次需要文件搜索时在后台建索引。索引是会话级的，
// 关闭插件或重建缓存后重新构建。
func (s *launcherSession) ensureFileIndex() {
	if s == nil || s.app == nil || !s.app.settings.LauncherSearchFiles {
		return
	}
	if s.files != nil && (s.files.built || s.files.building) {
		return
	}
	if s.win == nil || s.closed {
		return
	}
	if s.files == nil {
		s.files = &launcherFileIndex{}
	}
	s.files.building = true
	// App.Path 在主线程取好再交给 goroutine，避免平台路径 API 的线程约束。
	opts := defaultLauncherIndexOptions()
	win := s.win
	gen := s.gen
	go func() {
		entries, stats := buildLauncherFileIndex(opts)
		win.Update(func() {
			if s.closed || s.app.quitting || s.gen != gen || s.files == nil {
				return
			}
			s.files.entries = entries
			s.files.stats = stats
			s.files.building = false
			s.files.built = true
			log.Printf("启动器文件索引：%d 个文件，%d 个目录，跳过 %d，错误 %d，截断 %v",
				stats.files, stats.dirs, stats.skipped, stats.errors, stats.truncated)
			s.rebuildResults()
			s.app.refreshMain()
		})
	}()
}

// prefetchIcons 只为当前结果中的应用取图标。FileIcon 内部会切到主线程，
// 所以它绝不能出现在 view() 的无条件渲染路径里。
func (s *launcherSession) prefetchIcons(items []launcherItem) {
	if s == nil || s.loadAppIcon == nil || s.win == nil || s.closed {
		return
	}
	if s.icons == nil {
		s.icons = make(map[string]*launcherIcon)
	}
	if s.iconSem == nil {
		s.iconSem = make(chan struct{}, launcherIconConcurrency)
	}
	started := 0
	for _, it := range items {
		if it.kind != launcherKindApp || it.path == "" || started >= launcherMaxResults {
			continue
		}
		if ic := s.icons[it.path]; ic != nil && (ic.bmp != nil || ic.loading || ic.failed) {
			continue
		}
		if len(s.icons) >= launcherIconCacheMax {
			// 长期运行后缓存可能变大；清空比做 LRU 更简单，未命中的图标会重新加载。
			s.icons = make(map[string]*launcherIcon)
		}
		s.icons[it.path] = &launcherIcon{loading: true}
		s.loadIcon(it.path, s.gen, s.win)
		started++
	}
}

func (s *launcherSession) loadIcon(path string, gen int, win *mygo.Window) {
	sem := s.iconSem
	go func() {
		sem <- struct{}{}
		defer func() { <-sem }()

		png, err := s.loadAppIcon(path, launcherIconSize)
		win.Update(func() {
			if s.closed || s.app.quitting || s.gen != gen {
				return
			}
			ic := s.icons[path]
			if ic == nil {
				return
			}
			ic.loading = false
			if err != nil || len(png) == 0 {
				ic.failed = true
				return
			}
			// 解码放在主线程，避免对 ui 类型的线程假设。
			bmp, derr := ui.DecodeBitmap(png)
			if derr != nil {
				ic.failed = true
				return
			}
			ic.bmp = bmp
		})
	}()
}

func (s *launcherSession) appIcon(path string) *ui.Bitmap {
	if s == nil || s.icons == nil {
		return nil
	}
	if ic := s.icons[path]; ic != nil {
		return ic.bmp
	}
	return nil
}

// rebuildCaches 丢弃应用/文件/图标缓存并重新加载。旧的 goroutine 结果
// 会因为 gen 不匹配被丢弃。
func (s *launcherSession) rebuildCaches() {
	if s == nil {
		return
	}
	s.gen++
	s.apps = nil
	s.appsLoaded = false
	s.appsLoading = false
	s.appsErr = ""
	s.files = nil
	s.icons = make(map[string]*launcherIcon)
	s.iconSem = make(chan struct{}, launcherIconConcurrency)
	s.rebuildResults()
	s.ensureApps()
	s.ensureFileIndex()
	s.refresh()
	s.app.refreshMain()
}
