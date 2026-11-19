package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const tinyPNGMaxResponseSize = 128 << 20 // 防止异常响应把内存撑爆。

// tinyPNGEndpoint 是包级变量，测试用 httptest 替换，不访问真实服务。
var tinyPNGEndpoint = "https://api.tinify.com/shrink"

type tinyPNGCompressor struct {
	client *http.Client
	keys   []TinyPNGKey
}

type tinyPNGReply struct {
	Input struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"input"`
	Output struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"output"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func newTinyPNGCompressor(client *http.Client, keys []TinyPNGKey) *tinyPNGCompressor {
	if client == nil {
		client = newHTTPClient(false)
		client.Timeout = 120 * time.Second
	}
	return &tinyPNGCompressor{client: client, keys: keys}
}

func (t *tinyPNGCompressor) Compress(ctx context.Context, input []byte, format string, opts imageCompressOptions) (imageCompressResult, error) {
	if len(t.keys) == 0 {
		return imageCompressResult{}, fmt.Errorf("请先在压缩设置中添加 TinyPNG API Key")
	}
	var failures []string
	for i, item := range t.keys {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		result, retry, err := t.compressWithKey(ctx, input, format, opts, key)
		if err == nil {
			result.KeyIndex = i
			return result, nil
		}
		if ctx.Err() != nil {
			return imageCompressResult{}, ctx.Err()
		}
		if !retry {
			return imageCompressResult{}, err
		}
		failures = append(failures, fmt.Sprintf("%s：%v", maskTinyPNGKey(key), err))
	}
	if len(failures) > 0 {
		return imageCompressResult{}, fmt.Errorf("TinyPNG 不可用（%s）", strings.Join(failures, "；"))
	}
	return imageCompressResult{}, fmt.Errorf("请先填写有效的 TinyPNG API Key")
}

// compressWithKey 的 retry 仅对无效 Key 和配额耗尽为 true；普通网络错误
// 不应被多 Key 掩盖成一次又一次相同的上传。
func (t *tinyPNGCompressor) compressWithKey(ctx context.Context, input []byte, format string, opts imageCompressOptions, key string) (imageCompressResult, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tinyPNGEndpoint, bytes.NewReader(input))
	if err != nil {
		return imageCompressResult{}, false, err
	}
	req.SetBasicAuth("api", key)
	req.Header.Set("Content-Type", "image/"+format)
	resp, err := t.client.Do(req)
	if err != nil {
		return imageCompressResult{}, false, fmt.Errorf("上传图片失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return imageCompressResult{}, true, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var reply tinyPNGReply
	if err := decodeTinyPNGReply(resp.Body, &reply); err != nil {
		return imageCompressResult{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(reply.Message)
		if msg == "" {
			msg = strings.TrimSpace(reply.Error)
		}
		if msg == "" {
			msg = resp.Status
		}
		return imageCompressResult{}, false, fmt.Errorf("TinyPNG：%s", msg)
	}
	if reply.Output.URL == "" {
		return imageCompressResult{}, false, fmt.Errorf("TinyPNG 未返回压缩结果")
	}
	usage, _ := strconv.Atoi(resp.Header.Get("Compression-Count"))

	outputURL := reply.Output.URL
	width, height := reply.Output.Width, reply.Output.Height
	if opts.MaxEdge > 0 {
		targetW, targetH := scaledSize(reply.Input.Width, reply.Input.Height, opts.MaxEdge)
		if targetW != reply.Input.Width || targetH != reply.Input.Height {
			resized, err := t.resize(ctx, outputURL, key, targetW, targetH)
			if err != nil {
				return imageCompressResult{}, false, err
			}
			outputURL = resized.Output.URL
			width, height = resized.Output.Width, resized.Output.Height
			if width == 0 || height == 0 {
				width, height = targetW, targetH
			}
		}
	}
	data, err := t.download(ctx, outputURL, key)
	if err != nil {
		return imageCompressResult{}, false, err
	}
	return imageCompressResult{Data: data, Width: width, Height: height, Usage: usage, KeyIndex: -1}, false, nil
}

func (t *tinyPNGCompressor) resize(ctx context.Context, url, key string, width, height int) (tinyPNGReply, error) {
	body, _ := json.Marshal(map[string]any{
		"resize": map[string]any{"method": "fit", "width": width, "height": height},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return tinyPNGReply{}, err
	}
	req.SetBasicAuth("api", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return tinyPNGReply{}, fmt.Errorf("TinyPNG 缩放失败：%w", err)
	}
	defer resp.Body.Close()
	var reply tinyPNGReply
	if err := decodeTinyPNGReply(resp.Body, &reply); err != nil {
		return tinyPNGReply{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || reply.Output.URL == "" {
		return tinyPNGReply{}, fmt.Errorf("TinyPNG 缩放失败：%s", resp.Status)
	}
	return reply, nil
}

func (t *tinyPNGCompressor) download(ctx context.Context, url, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("api", key)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载压缩结果失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载压缩结果失败：HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, tinyPNGMaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取压缩结果失败：%w", err)
	}
	if len(data) > tinyPNGMaxResponseSize {
		return nil, fmt.Errorf("压缩结果超过 128MB，已中止下载")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("TinyPNG 返回了空图片")
	}
	return data, nil
}

func decodeTinyPNGReply(r io.Reader, reply *tinyPNGReply) error {
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(reply); err != nil {
		return fmt.Errorf("解析 TinyPNG 响应失败：%w", err)
	}
	return nil
}

func maskTinyPNGKey(key string) string {
	if len(key) <= 6 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-2:]
}
