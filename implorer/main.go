package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

type App struct{}

func (a *App) GetWindows() []WindowInfo {
	return enumTopLevelWindows()
}

func (a *App) ActivateWindow(hwnd uintptr) {
	activateWindow(hwnd)
}

func (a *App) LaunchApp(path string) error {
	return launchProcess(path)
}

// webView2Path returns the path to the bundled WebView2 fixed-version
// runtime. In a WinPE image this is X:\winkit\webview2. Falls back to
// empty string (system-installed WebView2).
func webView2Path() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(exe), "webview2")
	if _, err := os.Stat(filepath.Join(candidate, "msedgewebview2.exe")); err == nil {
		return candidate
	}
	return ""
}

// webView2UserData returns a writable directory for the WebView2 browser
// profile. In WinPE the default %APPDATA% lands on the read-only ramdisk;
// we redirect to the writable NTFS volume instead.
func webView2UserData() string {
	for _, d := range []byte{'E', 'D'} {
		dir := fmt.Sprintf("%c:\\implorer-webview2", d)
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir
		}
	}
	return ""
}

func rendererMode() string {
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--renderer=") {
			return strings.TrimPrefix(arg, "--renderer=")
		}
	}
	if v := os.Getenv("IMPLORER_RENDERER"); v != "" {
		return v
	}
	return "auto"
}

func main() {
	runtime.LockOSThread()

	mode := rendererMode()
	if mode == "contentshell" {
		if err := contentShellFallback(); err != nil {
			log.Fatal(err)
		}
		return
	}

	app := application.New(application.Options{
		Name: "implorer",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Services: []application.Service{
			application.NewService(&App{}),
		},
		Windows: application.WindowsOptions{
			WebviewBrowserPath:  webView2Path(),
			WebviewUserDataPath: webView2UserData(),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:        "desktop",
		Title:       "",
		Frameless:   true,
		Width:       1920,
		Height:      1080,
		URL:         "/desktop.html",
		AlwaysOnTop: false,
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:        "taskbar",
		Title:       "",
		Frameless:   true,
		Width:       1920,
		Height:      48,
		URL:         "/taskbar.html",
		AlwaysOnTop: true,
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
	})

	app.SystemTray.New()

	if err := app.Run(); err != nil {
		if mode == "auto" && findContentShell() != "" {
			log.Printf("Wails failed (%v), falling back to content-shell", err)
			if err := contentShellFallback(); err != nil {
				log.Fatal(err)
			}
			return
		}
		log.Fatal(err)
	}
}
