package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// receiveFile copies stdin to path, creating parent directories. It is the
// guest half of `winkit cp`: gosshd pipes the session's stdin into the
// command, and cmd.exe has no tool that copies binary stdin to a file
// intact. The file is written to a temporary name and renamed so a
// dropped connection never leaves a truncated file at the destination.
func receiveFile(in io.Reader, path string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".winkit-partial"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, in)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return n, fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return n, err
	}
	return n, nil
}
