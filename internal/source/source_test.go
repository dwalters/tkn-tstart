package source

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// ── test transport ────────────────────────────────────────────────────────────

type mockTransport struct {
	// keyed by exact URL string
	responses map[string]mockResponse
	// ordered list of URLs that were requested
	Requests []string
}

type mockResponse struct {
	status int
	body   string
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.Requests = append(m.Requests, req.URL.String())
	r, ok := m.responses[req.URL.String()]
	if !ok {
		r = mockResponse{404, "not found"}
	}
	return &http.Response{
		StatusCode: r.status,
		Status:     fmt.Sprintf("%d %s", r.status, http.StatusText(r.status)),
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     make(http.Header),
	}, nil
}

func withMock(responses map[string]mockResponse) (transport *mockTransport, restore func()) {
	tr := &mockTransport{responses: responses}
	prev := httpClient
	httpClient = &http.Client{Transport: tr}
	return tr, func() { httpClient = prev }
}

// ── local / file:// ───────────────────────────────────────────────────────────

func TestFetch_LocalFile(t *testing.T) {
	f, err := os.CreateTemp("", "manifest-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("kind: TaskRun")
	f.Close()

	data, err := Fetch(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "kind: TaskRun" {
		t.Errorf("got %q", data)
	}
}

func TestFetch_FileSchemeURL(t *testing.T) {
	f, err := os.CreateTemp("", "manifest-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("kind: PipelineRun")
	f.Close()

	data, err := Fetch("file://" + f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "kind: PipelineRun" {
		t.Errorf("got %q", data)
	}
}

func TestFetch_LocalFile_Missing(t *testing.T) {
	_, err := Fetch("/no/such/file.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFetch_UnknownScheme(t *testing.T) {
	_, err := Fetch("ftp://example.com/run.yaml")
	if err == nil {
		t.Fatal("expected error for unknown scheme")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ── generic HTTPS ─────────────────────────────────────────────────────────────

func TestFetch_GenericHTTPS(t *testing.T) {
	const body = "kind: TaskRun"
	const target = "https://example.com/run.yaml"

	_, restore := withMock(map[string]mockResponse{
		target: {200, body},
	})
	defer restore()

	data, err := Fetch(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != body {
		t.Errorf("got %q", data)
	}
}

func TestFetch_HTTP404(t *testing.T) {
	const target = "https://example.com/missing.yaml"
	_, restore := withMock(map[string]mockResponse{
		target: {404, "not found"},
	})
	defer restore()

	_, err := Fetch(target)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got: %v", err)
	}
}

// ── GitHub blob URL transformation ───────────────────────────────────────────

func TestFetch_GitHubBlobURL(t *testing.T) {
	const content = "kind: TaskRun"
	const rawURL = "https://raw.githubusercontent.com/alice/myrepo/main/run.yaml"

	tr, restore := withMock(map[string]mockResponse{
		rawURL: {200, content},
	})
	defer restore()

	data, err := Fetch("https://github.com/alice/myrepo/blob/main/run.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != content {
		t.Errorf("got %q", data)
	}
	// Verify the URL was transformed, not fetched from github.com directly.
	if len(tr.Requests) != 1 || tr.Requests[0] != rawURL {
		t.Errorf("expected request to %s, got %v", rawURL, tr.Requests)
	}
}

func TestFetch_GitHubRawURL(t *testing.T) {
	const rawURL = "https://raw.githubusercontent.com/alice/myrepo/main/run.yaml"
	tr, restore := withMock(map[string]mockResponse{
		rawURL: {200, "kind: TaskRun"},
	})
	defer restore()

	_, err := Fetch(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Requests) != 1 || tr.Requests[0] != rawURL {
		t.Errorf("expected request to %s, got %v", rawURL, tr.Requests)
	}
}

// ── GitHub Enterprise blob URL transformation ─────────────────────────────────

func TestFetch_GHEBlobURL(t *testing.T) {
	const content = "kind: PipelineRun"
	// GHE blob → raw: /blob/ becomes /raw/
	const rawURL = "https://github.example.com/team/repo/raw/release/pipeline.yaml"

	tr, restore := withMock(map[string]mockResponse{
		rawURL: {200, content},
	})
	defer restore()

	data, err := Fetch("https://github.example.com/team/repo/blob/release/pipeline.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != content {
		t.Errorf("got %q", data)
	}
	if len(tr.Requests) != 1 || tr.Requests[0] != rawURL {
		t.Errorf("expected request to %s, got %v", rawURL, tr.Requests)
	}
}

// ── GitHub Gist ───────────────────────────────────────────────────────────────

func gistAPIResponse(files map[string]struct{ filename, content string }) string {
	type gistFile struct {
		Filename  string `json:"filename"`
		Content   string `json:"content"`
		RawURL    string `json:"raw_url"`
		Truncated bool   `json:"truncated"`
	}
	m := map[string]gistFile{}
	for k, v := range files {
		m[k] = gistFile{Filename: v.filename, Content: v.content, RawURL: "https://gist.githubusercontent.com/raw/" + k}
	}
	b, _ := json.Marshal(map[string]interface{}{"files": m})
	return string(b)
}

func TestFetch_GistPage_YAMLFile(t *testing.T) {
	const gistID = "abc123def456"
	const yamlContent = "kind: TaskRun"
	const apiURL = "https://api.github.com/gists/" + gistID

	_, restore := withMock(map[string]mockResponse{
		apiURL: {200, gistAPIResponse(map[string]struct{ filename, content string }{
			"run.yaml": {"run.yaml", yamlContent},
			"notes.md": {"notes.md", "some notes"},
		})},
	})
	defer restore()

	data, err := Fetch("https://gist.github.com/alice/" + gistID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != yamlContent {
		t.Errorf("got %q, want %q", data, yamlContent)
	}
}

func TestFetch_GistPage_PreferYAMLOverOther(t *testing.T) {
	const gistID = "deadbeef1234"
	const apiURL = "https://api.github.com/gists/" + gistID

	_, restore := withMock(map[string]mockResponse{
		apiURL: {200, gistAPIResponse(map[string]struct{ filename, content string }{
			"readme.txt": {"readme.txt", "plain text"},
			"task.yml":   {"task.yml", "kind: TaskRun"},
		})},
	})
	defer restore()

	data, err := Fetch("https://gist.github.com/alice/" + gistID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "kind: TaskRun" {
		t.Errorf("expected YAML file content, got %q", data)
	}
}

func TestFetch_GistPage_FallbackToFirstFile(t *testing.T) {
	const gistID = "cafebabe5678"
	const apiURL = "https://api.github.com/gists/" + gistID

	_, restore := withMock(map[string]mockResponse{
		apiURL: {200, gistAPIResponse(map[string]struct{ filename, content string }{
			"run.json": {"run.json", `{"kind":"TaskRun"}`},
		})},
	})
	defer restore()

	data, err := Fetch("https://gist.github.com/alice/" + gistID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"kind":"TaskRun"}` {
		t.Errorf("got %q", data)
	}
}

func TestFetch_GistRawURL(t *testing.T) {
	const rawURL = "https://gist.github.com/alice/abc123/raw/HEAD/run.yaml"
	tr, restore := withMock(map[string]mockResponse{
		rawURL: {200, "kind: TaskRun"},
	})
	defer restore()

	_, err := Fetch(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	// Must fetch the raw URL directly, not go through the API.
	if len(tr.Requests) != 1 || tr.Requests[0] != rawURL {
		t.Errorf("expected direct raw fetch, got %v", tr.Requests)
	}
}

func TestFetch_GistTruncated(t *testing.T) {
	const gistID = "abcdef012345"
	const apiURL = "https://api.github.com/gists/" + gistID
	const rawContentURL = "https://gist.githubusercontent.com/raw/run.yaml"

	// Build a gist API response with truncated=true
	apiBody, _ := json.Marshal(map[string]interface{}{
		"files": map[string]interface{}{
			"run.yaml": map[string]interface{}{
				"filename":  "run.yaml",
				"content":   "truncated...",
				"raw_url":   rawContentURL,
				"truncated": true,
			},
		},
	})

	_, restore := withMock(map[string]mockResponse{
		apiURL:        {200, string(apiBody)},
		rawContentURL: {200, "kind: TaskRun"},
	})
	defer restore()

	data, err := Fetch("https://gist.github.com/alice/" + gistID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "kind: TaskRun" {
		t.Errorf("expected full content from raw_url, got %q", data)
	}
}

func TestFetch_GistEmpty(t *testing.T) {
	const gistID = "empty00000"
	const apiURL = "https://api.github.com/gists/" + gistID

	_, restore := withMock(map[string]mockResponse{
		apiURL: {200, `{"files":{}}`},
	})
	defer restore()

	_, err := Fetch("https://gist.github.com/alice/" + gistID)
	if err == nil {
		t.Fatal("expected error for gist with no files")
	}
}
