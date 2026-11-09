//go:build !darwin

package main

func screenRecordingGranted() bool { return true }
func requestScreenRecording() bool { return true }
