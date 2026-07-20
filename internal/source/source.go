// Package source loads manifest YAML from a local path or URL.
// GitHub and GitHub Enterprise URLs are authenticated via the gh CLI
// credential store (respects GH_TOKEN, GITHUB_TOKEN, and ~/.config/gh/hosts.yml).
package source

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/cli/go-gh/v2/pkg/auth"
)

// httpClient is used for all outbound requests. Override in tests.
var httpClient = &http.Client{}

// Fetch retrieves YAML manifest content from location, which may be:
//   - A local file path (no scheme, or file://)
//   - A generic HTTPS URL
//   - A GitHub blob URL:  https://github.com/user/repo/blob/branch/path
//   - A GitHub raw URL:   https://raw.githubusercontent.com/user/repo/branch/path
//   - A GitHub Gist URL:  https://gist.github.com/user/<id>  (picks first YAML file)
//   - A GitHub Gist raw:  https://gist.github.com/user/<id>/raw/<rev>/<file>
//   - A GHE blob URL:     https://<host>/user/repo/blob/branch/path
//   - Any HTTPS URL where the gh CLI has stored credentials for that host
func Fetch(location string) ([]byte, error) {
	if !strings.Contains(location, "://") {
		return os.ReadFile(location)
	}

	u, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	switch u.Scheme {
	case "file":
		return os.ReadFile(u.Path)
	case "http", "https":
		return fetchHTTPS(u)
	default:
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
}

// Patterns for GitHub URL shapes.
var (
	// /user/repo/blob/branch/path/to/file.yaml
	blobPathRE = regexp.MustCompile(`^/([^/]+)/([^/]+)/blob/(.+)$`)

	// gist page: /user/<hex-id>  (possibly with trailing slash or fragment)
	gistPageRE = regexp.MustCompile(`^/[^/]+/([0-9a-f]+)/?$`)

	// gist raw:  /user/<hex-id>/raw/...
	gistRawRE = regexp.MustCompile(`^/[^/]+/[0-9a-f]+/raw/`)
)

func fetchHTTPS(u *url.URL) ([]byte, error) {
	host := u.Hostname()

	switch {
	// ── github.com gist ──────────────────────────────────────────────────────
	case host == "gist.github.com":
		tok, _ := auth.TokenForHost("github.com")
		return fetchGist(u, tok, "api.github.com")

	// ── github.com blob → raw.githubusercontent.com ──────────────────────────
	case host == "github.com":
		tok, _ := auth.TokenForHost("github.com")
		if m := blobPathRE.FindStringSubmatch(u.Path); m != nil {
			raw := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", m[1], m[2], m[3])
			return fetchRaw(raw, tok)
		}
		return fetchRaw(u.String(), tok)

	// ── raw.githubusercontent.com ─────────────────────────────────────────────
	case host == "raw.githubusercontent.com":
		tok, _ := auth.TokenForHost("github.com")
		return fetchRaw(u.String(), tok)

	// ── GitHub Enterprise (any host with gh credentials) ─────────────────────
	default:
		tok, _ := auth.TokenForHost(host)
		// Transform /user/repo/blob/branch/path → /user/repo/raw/branch/path
		if m := blobPathRE.FindStringSubmatch(u.Path); m != nil {
			u2 := *u
			u2.Path = fmt.Sprintf("/%s/%s/raw/%s", m[1], m[2], m[3])
			return fetchRaw(u2.String(), tok)
		}
		return fetchRaw(u.String(), tok)
	}
}

// fetchGist fetches a gist via the GitHub (or GHE) REST API and returns the
// content of the first YAML file found, or the first file if none are YAML.
// If the URL is already a /raw/ link, it is fetched directly.
func fetchGist(u *url.URL, token, apiHost string) ([]byte, error) {
	// Already a raw gist content URL — fetch directly.
	if gistRawRE.MatchString(u.Path) {
		return fetchRaw(u.String(), token)
	}

	m := gistPageRE.FindStringSubmatch(u.Path)
	if m == nil {
		// Unrecognised gist path — try fetching as-is.
		return fetchRaw(u.String(), token)
	}
	gistID := m[1]

	apiURL := fmt.Sprintf("https://%s/gists/%s", apiHost, gistID)
	body, err := fetchRaw(apiURL, token)
	if err != nil {
		return nil, fmt.Errorf("gist %s: %w", gistID, err)
	}

	var resp struct {
		Files map[string]struct {
			Filename  string `json:"filename"`
			Content   string `json:"content"`
			RawURL    string `json:"raw_url"`
			Truncated bool   `json:"truncated"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("gist %s: parsing API response: %w", gistID, err)
	}
	if len(resp.Files) == 0 {
		return nil, fmt.Errorf("gist %s: no files", gistID)
	}

	// Score each file: prefer .yaml/.yml; within that prefer non-truncated.
	type candidate struct {
		score     int // higher is better
		content   string
		rawURL    string
		truncated bool
	}
	var best candidate
	for _, f := range resp.Files {
		score := 0
		lower := strings.ToLower(f.Filename)
		if strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
			score = 2
		} else {
			score = 1
		}
		if !f.Truncated {
			score++
		}
		if score > best.score {
			best = candidate{score, f.Content, f.RawURL, f.Truncated}
		}
	}

	if best.truncated {
		return fetchRaw(best.rawURL, token)
	}
	return []byte(best.content), nil
}

// fetchRaw performs a GET request, adding Bearer auth if token is non-empty.
func fetchRaw(rawURL, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", "tkn-tstart")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %s", rawURL, resp.Status)
	}

	return io.ReadAll(resp.Body)
}
