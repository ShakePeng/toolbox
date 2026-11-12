//go:build !darwin

package main

// 其他平台没有 Spotlight，应用名由系统接口直接给出。
func spotlightAppNames([]string) map[string]string { return nil }
