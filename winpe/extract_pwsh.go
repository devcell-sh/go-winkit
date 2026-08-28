package winpe

import (
	"archive/zip"
	"fmt"
	"io"
)

// ExtractPwshFiles reads a PowerShell zip and returns its contents as a map
// keyed by answer-volume paths (e.g. "/pwsh/pwsh.exe").
func ExtractPwshFiles(zipPath string) (map[string][]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening pwsh zip: %w", err)
	}
	defer r.Close()

	files := make(map[string][]byte)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s in zip: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s from zip: %w", f.Name, err)
		}
		volPath := "/" + PwshVolDir + "/" + f.Name
		files[volPath] = data
	}
	return files, nil
}
