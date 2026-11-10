//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"

	"github.com/egoist/mygo"
)

// captureRegion 用系统 screencapture 抓取一块屏幕区域到 PNG，并解码像素。
func captureRegion(_ mygo.Display, r mygo.Rectangle) ([]byte, image.Image, error) {
	f, err := os.CreateTemp("", "toolbox-cap-*.png")
	if err != nil {
		return nil, nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	rect := fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height)
	out, err := exec.Command("/usr/sbin/screencapture", "-x", "-t", "png", "-R", rect, path).CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("%v %s", err, bytes.TrimSpace(out))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("解码截图失败：%w", err)
	}
	return data, img, nil
}
