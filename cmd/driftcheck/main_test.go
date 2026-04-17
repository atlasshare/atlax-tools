package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to path under dir and returns the full path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// defaultMapping returns the production struct mapping used by the
// drift checker. Tests reuse it for realism.
func defaultMapping() map[string][]string {
	return map[string][]string{
		"RelayConfig":     {"RelayConfig"},
		"AgentConfig":     {"AgentConfig"},
		"ServerConfig":    {"RelayServer"},
		"TLSPaths":        {"RelayTLS", "AgentTLS"},
		"CustomerConfig":  {"Customer"},
		"PortConfig":      {"PortConfig"},
		"RelayConnection": {"AgentRelay"},
		"LogConfig":       {"LoggingConfig"},
		"MetricsConfig":   {"MetricsConfig"},
		"UpdateConfig":    {"UpdateConfig"},
		"ServiceMapping":  {"ServiceConfig"},
		"RateLimitConfig": {"RateLimitConfig"},
	}
}

func TestParseStructs_ExtractsNameAndYAMLTag(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type RelayServer struct {
	ListenAddr string ` + "`yaml:\"listen_addr\"`" + `
	MaxAgents  int    ` + "`yaml:\"max_agents\"`" + `
}
`
	path := writeFile(t, dir, "a.go", src)
	structs, err := parseStructs(path)
	if err != nil {
		t.Fatalf("parseStructs: %v", err)
	}
	fields, ok := structs["RelayServer"]
	if !ok {
		t.Fatalf("RelayServer not found")
	}
	if len(fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(fields))
	}
	if fields[0].Name != "ListenAddr" || fields[0].YAMLName != "listen_addr" {
		t.Errorf("field[0] = %+v", fields[0])
	}
	if fields[1].Name != "MaxAgents" || fields[1].YAMLName != "max_agents" {
		t.Errorf("field[1] = %+v", fields[1])
	}
}

func TestParseStructs_HandlesOmitemptyAndOptions(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type PortConfig struct {
	Port        int    ` + "`yaml:\"port\"`" + `
	Description string ` + "`yaml:\"description,omitempty\"`" + `
}
`
	path := writeFile(t, dir, "a.go", src)
	structs, err := parseStructs(path)
	if err != nil {
		t.Fatalf("parseStructs: %v", err)
	}
	fields := structs["PortConfig"]
	if fields[1].YAMLName != "description" {
		t.Errorf("want yaml name description (options stripped), got %q", fields[1].YAMLName)
	}
}

func TestParseStructs_IgnoresFieldsWithoutYAMLTag(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type Thing struct {
	Exported   string ` + "`yaml:\"exported\"`" + `
	NoTag      string
	OtherTag   string ` + "`json:\"other\"`" + `
}
`
	path := writeFile(t, dir, "a.go", src)
	structs, err := parseStructs(path)
	if err != nil {
		t.Fatalf("parseStructs: %v", err)
	}
	fields := structs["Thing"]
	if len(fields) != 1 {
		t.Fatalf("want 1 yaml-tagged field, got %d", len(fields))
	}
	if fields[0].Name != "Exported" {
		t.Errorf("unexpected field: %+v", fields[0])
	}
}

func TestParseStructs_IgnoresExplicitlySkippedFields(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type Thing struct {
	Kept    string ` + "`yaml:\"kept\"`" + `
	Skipped string ` + "`yaml:\"-\"`" + `
}
`
	path := writeFile(t, dir, "a.go", src)
	structs, err := parseStructs(path)
	if err != nil {
		t.Fatalf("parseStructs: %v", err)
	}
	if len(structs["Thing"]) != 1 || structs["Thing"][0].Name != "Kept" {
		t.Errorf("unexpected: %+v", structs["Thing"])
	}
}

func TestParseStructs_IgnoresEmbeddedAnonymousFields(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type Base struct {
	X string ` + "`yaml:\"x\"`" + `
}
type Child struct {
	Base
	Y string ` + "`yaml:\"y\"`" + `
}
`
	path := writeFile(t, dir, "a.go", src)
	structs, err := parseStructs(path)
	if err != nil {
		t.Fatalf("parseStructs: %v", err)
	}
	fields := structs["Child"]
	if len(fields) != 1 || fields[0].Name != "Y" {
		t.Fatalf("embedded field leaked or Y missing: %+v", fields)
	}
}

func TestParseStructs_HandlesMultipleNamesSameType(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type Pair struct {
	A, B string ` + "`yaml:\"shared\"`" + `
}
`
	path := writeFile(t, dir, "a.go", src)
	structs, err := parseStructs(path)
	if err != nil {
		t.Fatalf("parseStructs: %v", err)
	}
	fields := structs["Pair"]
	if len(fields) != 2 {
		t.Fatalf("want 2 fields from grouped declaration, got %d", len(fields))
	}
	if fields[0].Name != "A" || fields[1].Name != "B" {
		t.Errorf("unexpected grouped field names: %+v", fields)
	}
}

func TestCompare_NoDrift(t *testing.T) {
	dir := t.TempDir()
	src := `package config
type ServerConfig struct {
	ListenAddr string ` + "`yaml:\"listen_addr\"`" + `
}
`
	community := writeFile(t, dir, "community.go", src)

	toolsSrc := `package config
type RelayServer struct {
	ListenAddr string ` + "`yaml:\"listen_addr\"`" + `
}
`
	tools := writeFile(t, dir, "tools.go", toolsSrc)

	findings, err := runDriftCheck(community, tools, defaultMapping())
	if err != nil {
		t.Fatalf("runDriftCheck: %v", err)
	}
	for _, f := range findings {
		if f.severity() == severityError {
			t.Errorf("unexpected error finding: %+v", f)
		}
	}
}

func TestCompare_MissingFieldInTools(t *testing.T) {
	dir := t.TempDir()
	community := writeFile(t, dir, "community.go", `package config
type ServerConfig struct {
	ListenAddr  string `+"`yaml:\"listen_addr\"`"+`
	AdminSocket string `+"`yaml:\"admin_socket\"`"+`
}
`)
	tools := writeFile(t, dir, "tools.go", `package config
type RelayServer struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
}
`)
	findings, err := runDriftCheck(community, tools, defaultMapping())
	if err != nil {
		t.Fatalf("runDriftCheck: %v", err)
	}
	var missing *Finding
	for i := range findings {
		if findings[i].Kind == KindMissingField && findings[i].Field == "admin_socket" {
			missing = &findings[i]
			break
		}
	}
	if missing == nil {
		t.Fatalf("expected MissingField for admin_socket, got: %+v", findings)
	}
	if missing.ToolsStruct != "RelayServer" || missing.CommunityStruct != "ServerConfig" {
		t.Errorf("wrong struct mapping in finding: %+v", missing)
	}
}

func TestCompare_TagMismatch(t *testing.T) {
	dir := t.TempDir()
	community := writeFile(t, dir, "community.go", `package config
type ServerConfig struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
}
`)
	tools := writeFile(t, dir, "tools.go", `package config
type RelayServer struct {
	ListenAddr string `+"`yaml:\"listenaddr\"`"+`
}
`)
	findings, err := runDriftCheck(community, tools, defaultMapping())
	if err != nil {
		t.Fatalf("runDriftCheck: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Kind == KindTagMismatch && f.Field == "ListenAddr" {
			found = true
			if f.CommunityTag != "listen_addr" || f.ToolsTag != "listenaddr" {
				t.Errorf("bad tag comparison: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("expected TagMismatch on ListenAddr, got: %+v", findings)
	}
}

func TestCompare_ExtraInToolsIsWarning(t *testing.T) {
	dir := t.TempDir()
	community := writeFile(t, dir, "community.go", `package config
type ServerConfig struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
}
`)
	tools := writeFile(t, dir, "tools.go", `package config
type RelayServer struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
	ToolsOnly  string `+"`yaml:\"tools_only\"`"+`
}
`)
	findings, err := runDriftCheck(community, tools, defaultMapping())
	if err != nil {
		t.Fatalf("runDriftCheck: %v", err)
	}
	var warn *Finding
	for i := range findings {
		if findings[i].Kind == KindExtraInTools && findings[i].Field == "tools_only" {
			warn = &findings[i]
			break
		}
	}
	if warn == nil {
		t.Fatalf("expected ExtraInTools warning: %+v", findings)
	}
	if warn.severity() != severityWarn {
		t.Errorf("ExtraInTools should be warning, got severity %v", warn.severity())
	}
	// Should still exit 0.
	if hasErrors(findings) {
		t.Errorf("ExtraInTools should not trip failure: %+v", findings)
	}
}

func TestCompare_MissingStructInTools(t *testing.T) {
	dir := t.TempDir()
	community := writeFile(t, dir, "community.go", `package config
type ServerConfig struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
}
type RateLimitConfig struct {
	Burst int `+"`yaml:\"burst\"`"+`
}
`)
	tools := writeFile(t, dir, "tools.go", `package config
type RelayServer struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
}
`)
	findings, err := runDriftCheck(community, tools, defaultMapping())
	if err != nil {
		t.Fatalf("runDriftCheck: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Kind == KindMissingStruct && f.CommunityStruct == "RateLimitConfig" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected MissingStruct for RateLimitConfig: %+v", findings)
	}
}

func TestCompare_OmitemptyDivergence(t *testing.T) {
	dir := t.TempDir()
	community := writeFile(t, dir, "community.go", `package config
type ServerConfig struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
	MaxAgents  int    `+"`yaml:\"max_agents\"`"+`
}
`)
	tools := writeFile(t, dir, "tools.go", `package config
type RelayServer struct {
	ListenAddr string `+"`yaml:\"listen_addr\"`"+`
	MaxAgents  int    `+"`yaml:\"max_agents,omitempty\"`"+`
}
`)
	findings, err := runDriftCheck(community, tools, defaultMapping())
	if err != nil {
		t.Fatalf("runDriftCheck: %v", err)
	}
	var mismatch *Finding
	for i := range findings {
		if findings[i].Kind == KindTagOptionsMismatch && findings[i].Field == "MaxAgents" {
			mismatch = &findings[i]
			break
		}
	}
	if mismatch == nil {
		t.Fatalf("expected TagOptionsMismatch for MaxAgents, got: %+v", findings)
	}
	if mismatch.CommunityTag != "max_agents" {
		t.Errorf("want community tag %q, got %q", "max_agents", mismatch.CommunityTag)
	}
	if mismatch.ToolsTag != "max_agents,omitempty" {
		t.Errorf("want tools tag %q, got %q", "max_agents,omitempty", mismatch.ToolsTag)
	}
	if mismatch.severity() != severityWarn {
		t.Errorf("TagOptionsMismatch should be warning, got severity %v", mismatch.severity())
	}
	// Should still exit 0 -- omitempty divergence is not an error.
	if hasErrors(findings) {
		t.Errorf("omitempty divergence should not trip failure: %+v", findings)
	}
}

func TestRunDriftCheck_FailsOnParseError(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "bad.go", `package config
type Broken struct {
	// unterminated
`)
	ok := writeFile(t, dir, "ok.go", `package config
type RelayServer struct{}
`)
	if _, err := runDriftCheck(bad, ok, defaultMapping()); err == nil {
		t.Errorf("expected parse error, got nil")
	}
}

func TestRunDriftCheck_RejectsSuspiciousPath(t *testing.T) {
	// Clean-path guard: we don't want callers passing ".." traversal.
	dir := t.TempDir()
	ok := writeFile(t, dir, "ok.go", `package config
type RelayServer struct{}
`)
	_, err := runDriftCheck("../../../etc/passwd", ok, defaultMapping())
	if err == nil {
		t.Errorf("expected error for non-Go path")
	}
}
