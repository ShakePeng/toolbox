package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestTinyPNGKeyRotationAndUsage(t *testing.T) {
	var mu sync.Mutex
	var users []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, key, ok := r.BasicAuth()
		if !ok {
			t.Error("TinyPNG 请求缺少 Basic Auth")
		}
		mu.Lock()
		users = append(users, key)
		mu.Unlock()
		switch r.URL.Path {
		case "/shrink":
			if key == "bad-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Compression-Count", "42")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"input":  map[string]any{"width": 100, "height": 80},
				"output": map[string]any{"url": server.URL + "/output", "width": 100, "height": 80},
			})
		case "/output":
			_, _ = w.Write([]byte("compressed-image"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	oldEndpoint := tinyPNGEndpoint
	tinyPNGEndpoint = server.URL + "/shrink"
	defer func() { tinyPNGEndpoint = oldEndpoint }()

	engine := newTinyPNGCompressor(server.Client(), []TinyPNGKey{{Key: "bad-key"}, {Key: "good-key"}})
	result, err := engine.Compress(context.Background(), []byte("input"), imageFormatPNG, imageCompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "compressed-image" || result.KeyIndex != 1 || result.Usage != 42 {
		t.Fatalf("轮换或用量结果不正确：%+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(users, ",") != "bad-key,good-key,good-key" {
		t.Fatalf("请求认证顺序异常：%v", users)
	}
}

func TestTinyPNGResizeTransform(t *testing.T) {
	var server *httptest.Server
	resizeSeen := false
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shrink":
			w.Header().Set("Compression-Count", "7")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"input":  map[string]any{"width": 200, "height": 100},
				"output": map[string]any{"url": server.URL + "/original", "width": 200, "height": 100},
			})
		case "/original":
			if r.Method != http.MethodPost {
				t.Fatalf("缩放应 POST 到 output.url，实际 %s", r.Method)
			}
			var body map[string]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["resize"]["width"] != float64(40) || body["resize"]["height"] != float64(20) {
				t.Fatalf("缩放参数不正确：%+v", body)
			}
			resizeSeen = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{"url": server.URL + "/resized", "width": 40, "height": 20},
			})
		case "/resized":
			_, _ = w.Write([]byte("resized"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	oldEndpoint := tinyPNGEndpoint
	tinyPNGEndpoint = server.URL + "/shrink"
	defer func() { tinyPNGEndpoint = oldEndpoint }()

	engine := newTinyPNGCompressor(server.Client(), []TinyPNGKey{{Key: "key"}})
	result, err := engine.Compress(context.Background(), []byte("input"), imageFormatPNG, imageCompressOptions{MaxEdge: 40})
	if err != nil {
		t.Fatal(err)
	}
	if !resizeSeen || string(result.Data) != "resized" || result.Width != 40 || result.Height != 20 {
		t.Fatalf("云端缩放未生效：%+v, resize=%v", result, resizeSeen)
	}
}

func TestTinyPNGErrorsDoNotLeakFullKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	oldEndpoint := tinyPNGEndpoint
	tinyPNGEndpoint = server.URL
	defer func() { tinyPNGEndpoint = oldEndpoint }()

	key := "test-key-12345678"
	engine := newTinyPNGCompressor(server.Client(), []TinyPNGKey{{Key: key}})
	_, err := engine.Compress(context.Background(), []byte("input"), imageFormatPNG, imageCompressOptions{})
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("错误不能泄露完整 Key：%v", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("应保留配额错误上下文：%v", err)
	}
}

func TestTinyPNGRequiresKey(t *testing.T) {
	engine := newTinyPNGCompressor(nil, nil)
	_, err := engine.Compress(context.Background(), []byte("input"), imageFormatJPEG, imageCompressOptions{})
	if err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("缺少 Key 应给出配置提示：%v", err)
	}
}

func TestMaskTinyPNGKey(t *testing.T) {
	if got := maskTinyPNGKey("1234"); got != "***" {
		t.Fatal(got)
	}
	if got := fmt.Sprintf("%s", maskTinyPNGKey("12345678")); got != "1234***78" {
		t.Fatal(got)
	}
}
