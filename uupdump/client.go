package uupdump

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.uupdump.net"

type Client struct {
	baseURL    string
	httpClient *http.Client
	lastReq    time.Time
}

type Option func(*Client)

func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) request(ctx context.Context, endpoint string, params url.Values, out any) error {
	since := time.Since(c.lastReq)
	if since < 100*time.Microsecond {
		time.Sleep(100*time.Microsecond - since)
	}

	u := c.baseURL + "/" + endpoint
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}

	c.lastReq = time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var env responseEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decoding envelope: %w", err)
	}

	if err := env.checkError(); err != nil {
		return err
	}

	return json.Unmarshal(env.Response, out)
}

func (c *Client) ListBuilds(ctx context.Context, search string) ([]Build, error) {
	params := url.Values{
		"search":     {search},
		"sortByDate": {"1"},
	}

	var resp BuildsResponse
	if err := c.request(ctx, "listid.php", params, &resp); err != nil {
		return nil, err
	}
	return resp.Builds, nil
}

func (c *Client) ListLanguages(ctx context.Context, uuid string) (*DetailsResponse, error) {
	params := url.Values{"id": {uuid}}
	var resp DetailsResponse
	if err := c.request(ctx, "listlangs.php", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListEditions(ctx context.Context, uuid, lang string) (*EditionsResponse, error) {
	params := url.Values{"id": {uuid}, "lang": {lang}}
	var resp EditionsResponse
	if err := c.request(ctx, "listeditions.php", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetPackage(ctx context.Context, uuid, lang string, editions []string) (*PackageResponse, error) {
	params := url.Values{"id": {uuid}, "lang": {lang}}
	for _, ed := range editions {
		params.Add("edition[]", ed)
	}
	var resp PackageResponse
	if err := c.request(ctx, "get.php", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) FindLatestARM64(ctx context.Context) (*Build, error) {
	candidates, err := c.FindARM64Candidates(ctx)
	if err != nil {
		return nil, err
	}
	return &candidates[0], nil
}

// gaAnchorSearch names the newest Windows release known to publish
// complete, assemblable ESD sets. Preview branches (26H1 28000.x, 26H2
// 26300.x) publish install images whose resources live in .cab/.wim files
// this pipeline does not consume, and they crowd the GA builds out of the
// newest-first listing entirely. Bump when a newer release goes GA and
// proves assemblable.
const gaAnchorSearch = "windows 11 24h2 arm64"

// FindARM64Candidates returns non-delta ARM64 builds to try in order:
// the newest builds first, then the GA anchor's builds, deduplicated by
// build number. Callers should attempt them in order — a preview build's
// ESD set can be unassemblable and the only way to know is to try.
func (c *Client) FindARM64Candidates(ctx context.Context) ([]Build, error) {
	return c.FindARM64CandidatesSearch(ctx, "windows 11 arm64")
}

// FindARM64CandidatesSearch is FindARM64Candidates with a caller-chosen
// primary search; the GA anchor is still appended as the fallback tier.
func (c *Client) FindARM64CandidatesSearch(ctx context.Context, search string) ([]Build, error) {
	return c.mergeSearches(ctx, []string{search, gaAnchorSearch})
}

// FindCandidates lists the builds a resolved spec should attempt, in
// order: the spec's own searches (broad, then version-qualified for
// recall), then the GA anchor tier, deduplicated by build number.
func (c *Client) FindCandidates(ctx context.Context, r ResolvedSpec) ([]Build, error) {
	queries := r.SearchQueries()
	if r.Product == "11" {
		queries = append(queries, gaAnchorSearch)
	}
	return c.mergeSearches(ctx, queries)
}

// mergeSearches runs each listing search in order and merges the results,
// deduplicated by build. Only the first query is load-bearing: a later
// query failing just removes a safety net, so its error is dropped.
func (c *Client) mergeSearches(ctx context.Context, queries []string) ([]Build, error) {
	seenQuery := map[string]bool{}
	seen := map[string]bool{}
	var out []Build
	for i, q := range queries {
		if seenQuery[q] {
			continue
		}
		seenQuery[q] = true
		builds, err := c.arm64FullBuilds(ctx, q)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			continue
		}
		for _, b := range builds {
			if seen[b.Build] {
				continue
			}
			seen[b.Build] = true
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ARM64 builds found")
	}
	return out, nil
}

func (c *Client) arm64FullBuilds(ctx context.Context, search string) ([]Build, error) {
	builds, err := c.ListBuilds(ctx, search)
	if err != nil {
		return nil, fmt.Errorf("listing builds (%q): %w", search, err)
	}

	var arm64 []Build
	for _, b := range builds {
		if b.Arch == "arm64" {
			arm64 = append(arm64, b)
		}
	}

	sort.Slice(arm64, func(i, j int) bool {
		return arm64[i].Created > arm64[j].Created
	})

	// Cumulative/quality updates are delta ESDs — they reference blobs from
	// a base build we don't download. Feature updates have self-contained
	// ESDs.
	var full []Build
	for _, b := range arm64 {
		if !isDeltaBuild(b.Title) {
			full = append(full, b)
		}
	}
	if len(full) == 0 {
		return arm64, nil
	}
	return full, nil
}

// versionName matches Windows feature-update version names like 24H2.
var versionName = regexp.MustCompile(`^\d{2}h\d$`)

// BuildMatchesVersion reports whether a build belongs to the requested
// Windows version. Accepted forms, all case-insensitive:
//
//	""/"latest"  — any build
//	"24H2"       — version name; matches "version 24H2" in the title
//	"26100"      — build series prefix
//	"26100.9278" — exact build
//
// Version names are unique per release (24H2=26100.x, 25H2=26200.x,
// 26H2=26300.x, 26H1=28000.x), so the short name is sufficient.
func BuildMatchesVersion(b Build, version string) bool {
	v := strings.ToLower(strings.TrimSpace(version))
	switch {
	case v == "" || v == "latest":
		return true
	case versionName.MatchString(v):
		return strings.Contains(strings.ToLower(b.Title), "version "+v)
	default:
		return b.Build == v || strings.HasPrefix(b.Build, v+".")
	}
}

// kbNumber matches the KB article a servicing release is published under.
// The digits are required: "KB" alone is not a delta marker.
var kbNumber = regexp.MustCompile(`(?i)\bKB\d+`)

// isDeltaBuild reports whether a build's ESD references blobs from a base
// build, which we never download and so cannot assemble against.
//
// The word "update" alone cannot decide this: a cumulative or quality
// update is a delta, while a feature update ships a self-contained ESD.
// Servicing releases are also published under a KB number rather than the
// word "update" at all.
func isDeltaBuild(title string) bool {
	lower := strings.ToLower(title)

	// Checked before anything else: a feature update is full media despite
	// carrying both "update" and, at times, a KB number.
	if strings.Contains(lower, "feature update") {
		return false
	}

	if kbNumber.MatchString(title) {
		return true
	}
	return strings.Contains(lower, "cumulative update") ||
		strings.Contains(lower, "quality update")
}
