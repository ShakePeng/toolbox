//go:build !darwin && !windows

package main

import (
	"errors"
	"image"

	"github.com/egoist/mygo"
)

// captureRegion 在尚未适配的平台直接报错，避免静默返回空白截图。
func captureRegion(_ mygo.Display, _ mygo.Rectangle) ([]byte, image.Image, error) {
	return nil, nil, errors.New("当前平台暂不支持截图")
}
