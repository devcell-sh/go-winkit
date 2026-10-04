package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// contentShellFallback starts an HTTP server serving the embedded frontend
// assets and launches content-shell pointed at it. This is used when
// WebView2 cannot create a controller (e.g. WinPE without DWM composition).
func contentShellFallback() error {
	runtime.LockOSThread()

	distFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return fmt.Errorf("opening embedded assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(distFS)))

	app := &App{}
	mux.HandleFunc("/api/windows", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(app.GetWindows())
	})
	mux.HandleFunc("/api/activate", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ HWND uintptr }
		if json.NewDecoder(r.Body).Decode(&req) == nil {
			app.ActivateWindow(req.HWND)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/launch", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := app.LaunchApp(req.Path); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", addr.Port)
	log.Printf("asset server listening on %s", baseURL)

	go http.Serve(ln, mux)

	csPath := findContentShell()
	if csPath == "" {
		log.Printf("content-shell not found, serving assets at %s", baseURL)
		select {}
	}

	log.Printf("launching content-shell: %s", csPath)

	var wg sync.WaitGroup
	screenW, screenH := screenSize()
	taskbarH := 48
	for _, page := range []struct {
		url  string
		args []string
	}{
		{baseURL + "/desktop.html", []string{
			fmt.Sprintf("--window-size=%d,%d", screenW, screenH-taskbarH),
			"--window-position=0,0",
		}},
		{baseURL + "/taskbar.html", []string{
			fmt.Sprintf("--window-size=%d,%d", screenW, taskbarH),
			fmt.Sprintf("--window-position=0,%d", screenH-taskbarH),
		}},
	} {
		wg.Add(1)
		go func(url string, extra []string) {
			defer wg.Done()
			args := []string{
				"--no-sandbox",
				"--in-process-gpu",
				"--no-first-run",
				"--user-data-dir=" + contentShellDataDir(url),
				"--force-device-scale-factor=1",
				"--use-gl=angle",
				"--use-angle=swiftshader",
				"--enable-unsafe-swiftshader",
				"--enable-features=Vulkan",
			}
			args = append(args, extra...)
			args = append(args, url)
			cmd := exec.CommandContext(context.Background(), csPath, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = append(os.Environ(), swiftShaderEnv(csPath)...)
			if err := cmd.Run(); err != nil {
				log.Printf("content-shell exited: %v", err)
			}
		}(page.url, page.args)
	}
	wg.Wait()
	return nil
}

func findContentShell() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)

	searchDirs := []string{dir}
	if runtime.GOOS == "windows" {
		searchDirs = append(searchDirs, `X:\winkit`, `E:\winkit`)
	}
	for _, d := range searchDirs {
		for _, name := range []string{
			filepath.Join(d, "content-shell", "content_shell.exe"),
			filepath.Join(d, "content-shell", "content_shell"),
			filepath.Join(d, "chrome", "chrome.exe"),
			filepath.Join(d, "chrome", "chrome"),
		} {
			if _, err := os.Stat(name); err == nil {
				return name
			}
		}
	}

	if p, err := exec.LookPath("content_shell"); err == nil {
		return p
	}
	return ""
}

func screenSize() (int, int) {
	w, h := platformScreenSize()
	if w <= 0 || h <= 0 {
		return 1024, 768
	}
	return w, h
}

func contentShellDataDir(url string) string {
	name := "cs-" + strings.ReplaceAll(
		strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "127.0.0.1:"),
		"/", "-")
	for _, d := range []byte{'E', 'D'} {
		dir := fmt.Sprintf("%c:\\implorer-%s", d, name)
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir
		}
	}
	return os.TempDir()
}

func swiftShaderEnv(csPath string) []string {
	icd := filepath.Join(filepath.Dir(csPath), "vk_swiftshader_icd.json")
	if _, err := os.Stat(icd); err != nil {
		return nil
	}
	return []string{"VK_ICD_FILENAMES=" + icd}
}
