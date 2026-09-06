package qemu

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePPM(t *testing.T, path string, width, height int, r, g, b byte) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	header := []byte("P6\n")
	header = append(header, []byte(fmt.Sprintf("%d %d\n255\n", width, height))...)
	f.Write(header)
	for i := 0; i < width*height; i++ {
		f.Write([]byte{r, g, b})
	}
}

func TestConvertPPMtoPNG_BasicConversion(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "test.ppm")
	pngPath := filepath.Join(dir, "test.png")

	writePPM(t, ppmPath, 4, 4, 255, 0, 0)

	require.NoError(t, ConvertPPMtoPNG(ppmPath, pngPath))

	f, err := os.Open(pngPath)
	require.NoError(t, err)
	defer f.Close()

	img, err := png.Decode(f)
	require.NoError(t, err)
	assert.Equal(t, 4, img.Bounds().Dx())
	assert.Equal(t, 4, img.Bounds().Dy())
}

func TestConvertPPMtoPNG_PreservesPixels(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "test.ppm")
	pngPath := filepath.Join(dir, "test.png")

	writePPM(t, ppmPath, 2, 2, 0, 128, 255)

	require.NoError(t, ConvertPPMtoPNG(ppmPath, pngPath))

	f, err := os.Open(pngPath)
	require.NoError(t, err)
	defer f.Close()

	img, err := png.Decode(f)
	require.NoError(t, err)
	r, g, b, _ := img.At(0, 0).RGBA()
	assert.Equal(t, uint32(0), r>>8)
	assert.Equal(t, uint32(128), g>>8)
	assert.Equal(t, uint32(255), b>>8)
}

func TestConvertPPMtoPNG_WithComments(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "test.ppm")
	pngPath := filepath.Join(dir, "test.png")

	f, err := os.Create(ppmPath)
	require.NoError(t, err)
	f.Write([]byte("P6\n# comment\n2 2\n# another comment\n255\n"))
	for i := 0; i < 4; i++ {
		f.Write([]byte{100, 100, 100})
	}
	f.Close()

	require.NoError(t, ConvertPPMtoPNG(ppmPath, pngPath))
	info, err := os.Stat(pngPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestConvertPPMtoPNG_MissingFile(t *testing.T) {
	err := ConvertPPMtoPNG("/nonexistent.ppm", "/dev/null/out.png")
	assert.Error(t, err)
}

func TestBluePixelRatio_AllBlue(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "blue.ppm")
	writePPM(t, ppmPath, 10, 10, 0, 0, 200)

	ratio, err := BluePixelRatio(ppmPath)
	require.NoError(t, err)
	assert.Greater(t, ratio, 0.9)
}

func TestBluePixelRatio_NoBlue(t *testing.T) {
	dir := t.TempDir()
	ppmPath := filepath.Join(dir, "red.ppm")
	writePPM(t, ppmPath, 10, 10, 255, 0, 0)

	ratio, err := BluePixelRatio(ppmPath)
	require.NoError(t, err)
	assert.Equal(t, 0.0, ratio)
}

func TestScreenshotPath_Format(t *testing.T) {
	ts := time.Date(2026, 8, 29, 14, 30, 0, 0, time.UTC)
	path := ScreenshotPath("/results", ScreenSourceQMP, ts, "none", 5, 10, "png")
	assert.Equal(t, "/results/screenshots/qmp/20260829T143000Z-none-005-010.png", path)
}

func TestEnsureScreenshotDir_Creates(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, EnsureScreenshotDir(dir, ScreenSourceQMP))
	_, err := os.Stat(filepath.Join(dir, "screenshots", "qmp"))
	assert.NoError(t, err)
}
