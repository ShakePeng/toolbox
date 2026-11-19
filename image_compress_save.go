package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// tinyRename 在覆盖回滚测试中注入一次失败，验证原图能够恢复。
var tinyRename = os.Rename

// tinyOutputPath 在原图旁生成不冲突的 -tiny 文件名，绝不默认覆盖已有文件。
func tinyOutputPath(source string) (string, error) {
	ext := filepath.Ext(source)
	base := strings.TrimSuffix(filepath.Base(source), ext) + "-tiny"
	dir := filepath.Dir(source)
	for i := 1; ; i++ {
		name := base + ext
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		candidate := filepath.Join(dir, name)
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func saveTinyBeside(source string, data []byte) (string, error) {
	output, err := tinyOutputPath(source)
	if err != nil {
		return "", fmt.Errorf("选择输出文件失败：%w", err)
	}
	if err := writeNewImageFile(output, data); err != nil {
		return "", err
	}
	return output, nil
}

func writeNewImageFile(output string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(output), ".tiny-download-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := writeAndClose(tmp, data, 0600); err != nil {
		return err
	}
	// 输出名已经过 Lstat 检查；若此刻发生重名竞争，宁可失败也不覆盖。
	if _, err := os.Lstat(output); err == nil {
		return fmt.Errorf("输出文件已存在：%s", filepath.Base(output))
	} else if !os.IsNotExist(err) {
		return err
	}
	// Link 以原子方式创建不存在的目标，发生重名竞争也不会覆盖对方。
	if err := os.Link(tmpName, output); err != nil {
		return fmt.Errorf("保存图片失败：%w", err)
	}
	return nil
}

// overwriteTinyOriginal 用同目录临时文件和备份完成替换。Windows 的 Rename
// 不能直接覆盖已有文件，所以先把原图移到备份，失败时还能恢复。
func overwriteTinyOriginal(source, format string, data []byte) error {
	if _, err := decodeCompressImage(data, format); err != nil {
		return fmt.Errorf("压缩结果校验失败：%w", err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("读取原图信息失败：%w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(source), ".tiny-replace-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := writeAndClose(tmp, data, info.Mode().Perm()); err != nil {
		return err
	}
	backupFile, err := os.CreateTemp(filepath.Dir(source), ".tiny-backup-*")
	if err != nil {
		return fmt.Errorf("创建备份名失败：%w", err)
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := tinyRename(source, backup); err != nil {
		return fmt.Errorf("备份原图失败：%w", err)
	}
	if err := tinyRename(tmpName, source); err != nil {
		// 恢复失败要同时保留备份路径，不能把两个错误合并成一句“失败”。
		if restoreErr := tinyRename(backup, source); restoreErr != nil {
			return fmt.Errorf("替换失败且原图恢复失败，备份保留在 %s：%v；原错误：%w", backup, restoreErr, err)
		}
		return fmt.Errorf("替换原图失败，已恢复原图：%w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("图片已替换，但临时备份清理失败：%w", err)
	}
	return nil
}

func writeAndClose(file *os.File, data []byte, mode os.FileMode) error {
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("写入图片失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步图片失败：%w", err)
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("设置文件权限失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭图片失败：%w", err)
	}
	return nil
}

type tinyZIPItem struct {
	Name string
	Data []byte
}

func writeTinyZIP(output string, items []tinyZIPItem) error {
	if len(items) == 0 {
		return fmt.Errorf("请先勾选已完成的图片")
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".tiny-zip-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	zw := zip.NewWriter(tmp)
	used := map[string]bool{}
	for _, item := range items {
		name := uniqueZIPName(item.Name, used)
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return err
		}
		if _, err := w.Write(item.Data); err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := writeAndClose(tmp, nil, 0600); err != nil {
		return err
	}
	return replaceTinyFile(tmpName, output)
}

func uniqueZIPName(name string, used map[string]bool) string {
	name = path.Base(strings.ReplaceAll(filepath.Base(name), "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "image"
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 2; used[strings.ToLower(candidate)]; i++ {
		candidate = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

// replaceTinyFile 用于系统保存面板允许覆盖的 ZIP；若目标已存在，同样保留备份直到成功。
func replaceTinyFile(tmpName, output string) error {
	if _, err := os.Lstat(output); os.IsNotExist(err) {
		return tinyRename(tmpName, output)
	}
	backup := output + fmt.Sprintf(".tiny-backup-%d", time.Now().UnixNano())
	if err := tinyRename(output, backup); err != nil {
		return err
	}
	if err := tinyRename(tmpName, output); err != nil {
		if restoreErr := tinyRename(backup, output); restoreErr != nil {
			return fmt.Errorf("保存失败，原文件备份在 %s：%v", backup, restoreErr)
		}
		return err
	}
	return os.Remove(backup)
}
