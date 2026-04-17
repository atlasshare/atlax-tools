package caddy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultHeaders(t *testing.T) {
	t.Parallel()
	headers := DefaultHeaders()

	assert.Contains(t, headers, "Strict-Transport-Security")
	assert.Contains(t, headers, "X-Content-Type-Options")
	assert.Contains(t, headers, "X-Frame-Options")
	assert.Contains(t, headers, "Referrer-Policy")

	assert.Contains(t, headers["Strict-Transport-Security"], "max-age=31536000")
	assert.Contains(t, headers["X-Content-Type-Options"], "nosniff")
	assert.Contains(t, headers["X-Frame-Options"], "SAMEORIGIN")
}

func TestDefaultCaddyfilePath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/etc/caddy/Caddyfile", DefaultCaddyfilePath())
}

func TestNewServiceBlock(t *testing.T) {
	t.Parallel()
	b := NewServiceBlock("example.com", 18080)

	assert.Equal(t, "example.com", b.Domain)
	require.Len(t, b.Upstreams, 1)
	assert.Equal(t, "*", b.Upstreams[0].Path)
	assert.Equal(t, "localhost:18080", b.Upstreams[0].Backend)
	assert.True(t, b.EnableGzip)
	assert.NotEmpty(t, b.Headers)
}

func TestNewServiceBlockWithAPI(t *testing.T) {
	t.Parallel()
	b := NewServiceBlockWithAPI("example.com", 18080, 18070)

	assert.Equal(t, "example.com", b.Domain)
	require.Len(t, b.Upstreams, 2)

	// Order matters: specific API path must come before catch-all so
	// Caddy routes /api/* requests to the API backend.
	assert.Equal(t, "/api/*", b.Upstreams[0].Path)
	assert.Equal(t, "localhost:18070", b.Upstreams[0].Backend)
	assert.Equal(t, "*", b.Upstreams[1].Path)
	assert.Equal(t, "localhost:18080", b.Upstreams[1].Backend)

	assert.True(t, b.EnableGzip)
	assert.NotEmpty(t, b.Headers)
}

func TestBlock_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		block          Block
		contains       []string
		mustNotContain []string
	}{
		{
			name: "simple reverse proxy with catch-all path",
			block: Block{
				Domain: "simple.example.com",
				Upstreams: []Upstream{
					{Path: "*", Backend: "localhost:8080"},
				},
			},
			contains: []string{
				"simple.example.com {",
				"reverse_proxy localhost:8080",
				"}",
			},
			mustNotContain: []string{
				"encode gzip",
				"header {",
			},
		},
		{
			name: "empty path is treated as catch-all",
			block: Block{
				Domain: "empty.example.com",
				Upstreams: []Upstream{
					{Path: "", Backend: "localhost:9090"},
				},
			},
			contains: []string{
				"empty.example.com {",
				"reverse_proxy localhost:9090",
			},
			// Bare "reverse_proxy localhost:9090" must not carry a path argument.
			mustNotContain: []string{
				"reverse_proxy  localhost:9090",
			},
		},
		{
			name: "path-scoped reverse proxy renders path before backend",
			block: Block{
				Domain: "api.example.com",
				Upstreams: []Upstream{
					{Path: "/api/*", Backend: "localhost:3000"},
					{Path: "*", Backend: "localhost:8080"},
				},
			},
			contains: []string{
				"api.example.com {",
				"reverse_proxy /api/* localhost:3000",
				"reverse_proxy localhost:8080",
			},
		},
		{
			name: "gzip encode directive",
			block: Block{
				Domain:     "gz.example.com",
				Upstreams:  []Upstream{{Path: "*", Backend: "localhost:8080"}},
				EnableGzip: true,
			},
			contains: []string{
				"encode gzip zstd",
			},
		},
		{
			name: "headers block includes key-value pairs",
			block: Block{
				Domain:    "hdr.example.com",
				Upstreams: []Upstream{{Path: "*", Backend: "localhost:8080"}},
				Headers: map[string]string{
					"X-Custom": `"value"`,
				},
			},
			contains: []string{
				"header {",
				`X-Custom "value"`,
			},
		},
		{
			name: "full block with gzip and headers",
			block: Block{
				Domain:     "full.example.com",
				Upstreams:  []Upstream{{Path: "*", Backend: "localhost:8080"}},
				EnableGzip: true,
				Headers:    DefaultHeaders(),
			},
			contains: []string{
				"full.example.com {",
				"reverse_proxy localhost:8080",
				"encode gzip zstd",
				"header {",
				"Strict-Transport-Security",
				"X-Content-Type-Options",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := tc.block.Render()

			for _, want := range tc.contains {
				assert.Contains(t, out, want, "expected output to contain %q:\n%s", want, out)
			}
			for _, unwanted := range tc.mustNotContain {
				assert.NotContains(t, out, unwanted, "output must not contain %q:\n%s", unwanted, out)
			}
			// Always-on structural invariants.
			assert.True(t, strings.HasPrefix(out, tc.block.Domain+" {"),
				"rendered block should start with domain and opening brace")
			assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), "}"),
				"rendered block should end with a closing brace")
		})
	}
}

func TestAppendToFile_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caddyPath := filepath.Join(dir, "Caddyfile")

	block := NewServiceBlock("dry.example.com", 18080)
	err := AppendToFile(caddyPath, block, true)
	require.NoError(t, err)

	// Dry run must not create the file.
	_, statErr := os.Stat(caddyPath)
	assert.True(t, os.IsNotExist(statErr), "dry run must not create the Caddyfile")
}

func TestAppendToFile_CreatesWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caddyPath := filepath.Join(dir, "Caddyfile")

	block := NewServiceBlock("new.example.com", 18080)
	err := AppendToFile(caddyPath, block, false)
	require.NoError(t, err)

	data, err := os.ReadFile(caddyPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "new.example.com {")
	assert.Contains(t, string(data), "reverse_proxy localhost:18080")
}

func TestAppendToFile_AppendsToExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caddyPath := filepath.Join(dir, "Caddyfile")

	existing := "# existing content\nfirst.example.com {\n    reverse_proxy localhost:1000\n}\n"
	require.NoError(t, os.WriteFile(caddyPath, []byte(existing), 0o644))

	block := NewServiceBlock("second.example.com", 18080)
	err := AppendToFile(caddyPath, block, false)
	require.NoError(t, err)

	data, err := os.ReadFile(caddyPath)
	require.NoError(t, err)
	out := string(data)

	assert.Contains(t, out, "first.example.com {", "existing block should be preserved")
	assert.Contains(t, out, "second.example.com {", "new block should be appended")
	// Existing block must precede new block.
	firstIdx := strings.Index(out, "first.example.com {")
	secondIdx := strings.Index(out, "second.example.com {")
	require.Positive(t, firstIdx)
	require.Positive(t, secondIdx)
	assert.Less(t, firstIdx, secondIdx)
}

func TestAppendToFile_AppendsWhenExistingHasNoTrailingNewline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caddyPath := filepath.Join(dir, "Caddyfile")

	existing := "first.example.com {\n    reverse_proxy localhost:1000\n}" // no trailing newline
	require.NoError(t, os.WriteFile(caddyPath, []byte(existing), 0o644))

	block := NewServiceBlock("second.example.com", 18080)
	err := AppendToFile(caddyPath, block, false)
	require.NoError(t, err)

	data, err := os.ReadFile(caddyPath)
	require.NoError(t, err)
	out := string(data)
	assert.Contains(t, out, "first.example.com {")
	assert.Contains(t, out, "second.example.com {")
}

func TestAppendToFile_DuplicateDomainReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caddyPath := filepath.Join(dir, "Caddyfile")

	block := NewServiceBlock("dup.example.com", 18080)
	require.NoError(t, AppendToFile(caddyPath, block, false))

	err := AppendToFile(caddyPath, block, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already has a block")
}
