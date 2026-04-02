// Package caddy generates and manages Caddyfile blocks for reverse
// proxying services exposed through the atlax relay.
package caddy

import (
	"fmt"
	"os"
	"strings"

	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

// Block represents a single Caddy site block.
type Block struct {
	Domain     string
	Upstreams  []Upstream
	EnableGzip bool
	Headers    map[string]string
}

// Upstream maps a path pattern to a backend address.
type Upstream struct {
	Path    string // e.g., "/api/*" or "*" (catch-all)
	Backend string // e.g., "localhost:18080"
}

// DefaultHeaders returns the standard security headers.
func DefaultHeaders() map[string]string {
	return map[string]string{
		"Strict-Transport-Security": `"max-age=31536000; includeSubDomains; preload"`,
		"X-Content-Type-Options":    `"nosniff"`,
		"X-Frame-Options":           `"SAMEORIGIN"`,
		"Referrer-Policy":           `"strict-origin-when-cross-origin"`,
	}
}

// Render produces a Caddyfile block string.
func (b Block) Render() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s {\n", b.Domain))

	// Reverse proxy directives (order matters: specific paths first).
	for _, u := range b.Upstreams {
		if u.Path == "*" || u.Path == "" {
			sb.WriteString(fmt.Sprintf("    reverse_proxy %s\n", u.Backend))
		} else {
			sb.WriteString(fmt.Sprintf("    reverse_proxy %s %s\n", u.Path, u.Backend))
		}
	}

	if b.EnableGzip {
		sb.WriteString("\n    encode gzip zstd\n")
	}

	if len(b.Headers) > 0 {
		sb.WriteString("\n    header {\n")
		for k, v := range b.Headers {
			sb.WriteString(fmt.Sprintf("        %s %s\n", k, v))
		}
		sb.WriteString("    }\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

// AppendToFile appends a Caddy block to the Caddyfile.
func AppendToFile(caddyfilePath string, block Block, dryRun bool) error {
	rendered := block.Render()

	if dryRun {
		tui.DryRunf("Would append to %s:", caddyfilePath)
		fmt.Println(rendered)
		logger.Log("caddy", fmt.Sprintf("[dry-run] append block for %s", block.Domain))
		return nil
	}

	// Read existing content.
	existing, err := os.ReadFile(caddyfilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read Caddyfile: %w", err)
	}

	// Check if domain already has a block.
	if strings.Contains(string(existing), block.Domain+" {") {
		return fmt.Errorf("domain %q already has a block in %s", block.Domain, caddyfilePath)
	}

	// Append.
	content := string(existing)
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + rendered

	if err := os.WriteFile(caddyfilePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write Caddyfile: %w", err)
	}

	logger.Log("caddy", fmt.Sprintf("appended block for %s to %s", block.Domain, caddyfilePath))
	tui.Successf("Added Caddy block for %s", block.Domain)
	return nil
}

// NewServiceBlock creates a block for a new service.
func NewServiceBlock(domain string, relayPort int) Block {
	return Block{
		Domain: domain,
		Upstreams: []Upstream{
			{Path: "*", Backend: fmt.Sprintf("localhost:%d", relayPort)},
		},
		EnableGzip: true,
		Headers:    DefaultHeaders(),
	}
}

// NewServiceBlockWithAPI creates a block with a separate API path.
func NewServiceBlockWithAPI(domain string, webPort, apiPort int) Block {
	return Block{
		Domain: domain,
		Upstreams: []Upstream{
			{Path: "/api/*", Backend: fmt.Sprintf("localhost:%d", apiPort)},
			{Path: "*", Backend: fmt.Sprintf("localhost:%d", webPort)},
		},
		EnableGzip: true,
		Headers:    DefaultHeaders(),
	}
}

// DefaultCaddyfilePath returns the platform default Caddyfile location.
func DefaultCaddyfilePath() string {
	return "/etc/caddy/Caddyfile"
}
