package qemu

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConvertPPMtoPNG reads a PPM (P6 binary) file and writes a PNG.
func ConvertPPMtoPNG(ppmPath, pngPath string) error {
	f, err := os.Open(ppmPath)
	if err != nil {
		return fmt.Errorf("open PPM: %w", err)
	}
	defer f.Close()

	img, err := decodePPM(f)
	if err != nil {
		return fmt.Errorf("decode PPM: %w", err)
	}

	out, err := os.Create(pngPath)
	if err != nil {
		return fmt.Errorf("create PNG: %w", err)
	}
	defer out.Close()

	if err := png.Encode(out, img); err != nil {
		return fmt.Errorf("encode PNG: %w", err)
	}
	return nil
}

func decodePPM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)

	magic, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if magic != "P6" {
		return nil, fmt.Errorf("unsupported PPM format: %s (only P6 supported)", magic)
	}

	widthStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading width: %w", err)
	}
	heightStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading height: %w", err)
	}
	maxvalStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading maxval: %w", err)
	}

	var width, height, maxval int
	fmt.Sscanf(widthStr, "%d", &width)
	fmt.Sscanf(heightStr, "%d", &height)
	fmt.Sscanf(maxvalStr, "%d", &maxval)

	if width <= 0 || height <= 0 || maxval <= 0 || maxval > 65535 {
		return nil, fmt.Errorf("invalid PPM dimensions: %dx%d maxval=%d", width, height, maxval)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	if maxval <= 255 {
		row := make([]byte, width*3)
		for y := 0; y < height; y++ {
			if _, err := io.ReadFull(br, row); err != nil {
				return nil, fmt.Errorf("reading pixel row %d: %w", y, err)
			}
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, color.RGBA{
					R: row[x*3], G: row[x*3+1], B: row[x*3+2], A: 255,
				})
			}
		}
	} else {
		row := make([]byte, width*6)
		for y := 0; y < height; y++ {
			if _, err := io.ReadFull(br, row); err != nil {
				return nil, fmt.Errorf("reading pixel row %d: %w", y, err)
			}
			for x := 0; x < width; x++ {
				r16 := uint16(row[x*6])<<8 | uint16(row[x*6+1])
				g16 := uint16(row[x*6+2])<<8 | uint16(row[x*6+3])
				b16 := uint16(row[x*6+4])<<8 | uint16(row[x*6+5])
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(r16 >> 8), G: uint8(g16 >> 8), B: uint8(b16 >> 8), A: 255,
				})
			}
		}
	}

	return img, nil
}

func readPPMToken(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		ch, err := r.ReadByte()
		if err != nil {
			if b.Len() > 0 {
				return b.String(), nil
			}
			return "", err
		}
		if ch == '#' {
			for {
				c, err := r.ReadByte()
				if err != nil {
					return "", err
				}
				if c == '\n' {
					break
				}
			}
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if b.Len() > 0 {
				return b.String(), nil
			}
			continue
		}
		b.WriteByte(ch)
	}
}

// BluePixelRatio returns the fraction of pixels that are "blue" (Windows Setup
// installer background).
func BluePixelRatio(ppmPath string) (float64, error) {
	return pixelRatio(ppmPath, func(r, g, b int) bool {
		return b >= 120 && b > r+40 && b > g+30
	})
}

// WhitePixelRatio returns the fraction of near-white pixels.
func WhitePixelRatio(ppmPath string) (float64, error) {
	return pixelRatio(ppmPath, func(r, g, b int) bool {
		return r >= 230 && g >= 230 && b >= 230
	})
}

// WindowsPurpleRatio returns the fraction of pixels matching the Windows 11
// boot/setup backdrop.
func WindowsPurpleRatio(ppmPath string) (float64, error) {
	return pixelRatio(ppmPath, func(r, g, b int) bool {
		return b >= 60 && b <= 140 && r <= 70 && g <= 40 && b > r+20
	})
}

func pixelRatio(ppmPath string, match func(r, g, b int) bool) (float64, error) {
	f, err := os.Open(ppmPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	img, err := decodePPM(f)
	if err != nil {
		return 0, err
	}

	bounds := img.Bounds()
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return 0, nil
	}

	n := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if match(int(r>>8), int(g>>8), int(b>>8)) {
				n++
			}
		}
	}
	return float64(n) / float64(total), nil
}

// ScreenSource names how a frame was acquired.
type ScreenSource string

const (
	ScreenSourceQMP ScreenSource = "qmp"
	ScreenSourceRDP ScreenSource = "rdp"
)

// ScreenshotPath returns the path for a captured frame.
func ScreenshotPath(resultsDir string, source ScreenSource, now time.Time,
	screen string, screenSeq, globalSeq int, ext string) string {
	name := fmt.Sprintf("%s-%s-%03d-%03d.%s",
		now.UTC().Format("20060102T150405Z"), screen, screenSeq, globalSeq, ext)
	return filepath.Join(resultsDir, "screenshots", string(source), name)
}

// EnsureScreenshotDir creates the directory ScreenshotPath writes into.
func EnsureScreenshotDir(resultsDir string, source ScreenSource) error {
	return os.MkdirAll(filepath.Join(resultsDir, "screenshots", string(source)), 0o755)
}
