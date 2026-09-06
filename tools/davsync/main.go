// davsync is a minimal WebDAV tree sync client for guests that lack the
// Windows WebClient service — WinPE above all. Full Windows maps the
// webdavshare server as a drive via the built-in redirector; WinPE has no
// such service and no way to add one, so this tool covers the same server
// with plain HTTP: PROPFIND to list, GET to pull, MKCOL+PUT to push.
//
// Usage (in the guest, against the host's webdavshare server):
//
//	davsync pull -url http://10.0.2.2:9843/ -dir X:\project
//	davsync push -url http://10.0.2.2:9843/ -dir X:\project
//
// Pure stdlib, cross-compiles for windows/arm64 like gosshd.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	baseURL := fs.String("url", "", "WebDAV server URL, e.g. http://10.0.2.2:9843/")
	dir := fs.String("dir", "", "local directory to sync")
	fs.Parse(os.Args[2:])
	if *baseURL == "" || *dir == "" {
		usage()
	}

	var err error
	switch cmd {
	case "pull":
		err = pullTree(*baseURL, *dir)
	case "push":
		err = pushTree(*baseURL, *dir)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "davsync %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: davsync pull|push -url <server> -dir <local dir>")
	os.Exit(2)
}

var client = &http.Client{Timeout: 5 * time.Minute}

// davEntry is one remote filesystem entry discovered via PROPFIND.
type davEntry struct {
	// Path is the entry's path relative to the share root, slash-separated,
	// no leading slash.
	Path  string
	IsDir bool
}

// multistatus mirrors the PROPFIND response envelope. Namespace-qualified
// field names keep this correct regardless of the server's chosen prefix.
type multistatus struct {
	XMLName   xml.Name `xml:"DAV: multistatus"`
	Responses []struct {
		Href     string `xml:"DAV: href"`
		Propstat []struct {
			Prop struct {
				ResourceType struct {
					Collection *struct{} `xml:"DAV: collection"`
				} `xml:"DAV: resourcetype"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// listDir PROPFINDs one remote directory (Depth 1) and returns its entries,
// excluding the directory itself.
func listDir(baseURL, dirPath string) ([]davEntry, error) {
	u := joinURL(baseURL, dirPath) + "/"
	req, err := http.NewRequest("PROPFIND", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("PROPFIND %s: status %d", u, resp.StatusCode)
	}
	var ms multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("PROPFIND %s: parsing response: %w", u, err)
	}

	var entries []davEntry
	for _, r := range ms.Responses {
		href, err := url.PathUnescape(strings.TrimSpace(r.Href))
		if err != nil {
			return nil, fmt.Errorf("PROPFIND %s: bad href %q: %w", u, r.Href, err)
		}
		rel := strings.Trim(href, "/")
		self := strings.Trim(dirPath, "/")
		if rel == self { // the directory's own entry
			continue
		}
		isDir := false
		for _, ps := range r.Propstat {
			if ps.Prop.ResourceType.Collection != nil {
				isDir = true
			}
		}
		entries = append(entries, davEntry{Path: rel, IsDir: isDir})
	}
	return entries, nil
}

// pullTree downloads the entire share into dest.
func pullTree(baseURL, dest string) error {
	return pullDir(baseURL, "", dest)
}

func pullDir(baseURL, remoteDir, dest string) error {
	entries, err := listDir(baseURL, remoteDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		local := filepath.Join(dest, filepath.FromSlash(e.Path))
		if e.IsDir {
			if err := os.MkdirAll(local, 0o755); err != nil {
				return err
			}
			if err := pullDir(baseURL, e.Path, dest); err != nil {
				return err
			}
			continue
		}
		if err := pullFile(baseURL, e.Path, local); err != nil {
			return err
		}
	}
	return nil
}

func pullFile(baseURL, remotePath, local string) error {
	resp, err := client.Get(joinURL(baseURL, remotePath))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", remotePath, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	f, err := os.Create(local)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", local, err)
	}
	return f.Close()
}

// pushTree uploads dir's entire tree to the share.
func pushTree(baseURL, dir string) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		remote := filepath.ToSlash(rel)
		if d.IsDir() {
			return mkcol(baseURL, remote)
		}
		return putFile(baseURL, remote, p)
	})
}

func mkcol(baseURL, remoteDir string) error {
	req, err := http.NewRequest("MKCOL", joinURL(baseURL, remoteDir), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// 201 created; 405 already exists — both fine.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("MKCOL %s: status %d", remoteDir, resp.StatusCode)
	}
	return nil
}

func putFile(baseURL, remotePath, local string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequest(http.MethodPut, joinURL(baseURL, remotePath), f)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT %s: status %d", remotePath, resp.StatusCode)
	}
	return nil
}

func joinURL(baseURL, p string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path.Clean("/"+p), "/")
}
