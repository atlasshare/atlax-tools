// Package main implements driftcheck, a CI tool that detects divergence
// between the atlax-tools local config mirror (internal/config/manager.go)
// and the upstream atlax community config (pkg/config/config.go).
//
// The tool parses Go source files with go/ast and go/parser (stdlib only),
// extracts struct field name and YAML tag pairs, and compares pairs under
// a known struct-name mapping. It never executes or imports the parsed code.
//
// Field type comparison is intentionally out of scope: community uses
// time.Duration for several fields while tools uses string for YAML loose
// compatibility. YAML tag parity is the contract that matters.
//
// Exit codes:
//
//	0 -- no drift, or only ExtraInTools warnings
//	1 -- at least one MissingStruct, MissingField, or TagMismatch finding
//	2 -- usage or parse error
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Field captures a single YAML-tagged struct field.
type Field struct {
	Name     string // Go identifier (e.g. ListenAddr)
	YAMLName string // yaml tag base name, options stripped (e.g. listen_addr)
	Tag      string // full tag contents after yaml: key, including options
}

// FindingKind categorises a single drift report.
type FindingKind int

const (
	// KindMissingStruct: community has a mapped struct that tools lacks.
	KindMissingStruct FindingKind = iota
	// KindMissingField: community struct has a YAML field that tools lacks.
	KindMissingField
	// KindTagMismatch: same Go field name, different YAML tag.
	KindTagMismatch
	// KindExtraInTools: tools struct has a YAML field community lacks.
	KindExtraInTools
)

type severity int

const (
	severityWarn severity = iota
	severityError
)

// Finding is a single drift report produced by runDriftCheck.
type Finding struct {
	Kind            FindingKind
	CommunityStruct string
	ToolsStruct     string
	Field           string // Go identifier or YAML name depending on Kind
	CommunityTag    string // full yaml tag contents from community source
	ToolsTag        string // full yaml tag contents from tools source
	Detail          string
}

func (f Finding) severity() severity {
	if f.Kind == KindExtraInTools {
		return severityWarn
	}
	return severityError
}

// hasErrors returns true if any finding is at error severity.
func hasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.severity() == severityError {
			return true
		}
	}
	return false
}

// main wires flags and exits based on runDriftCheck.
func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// run is the testable entry point. It never calls os.Exit itself.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("driftcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaultCommunity := filepath.Join("..", "atlax", "pkg", "config", "config.go")
	defaultTools := filepath.Join("internal", "config", "manager.go")
	community := fs.String("community-path", defaultCommunity, "path to atlax community pkg/config/config.go")
	tools := fs.String("tools-path", defaultTools, "path to atlax-tools internal/config/manager.go")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	findings, err := runDriftCheck(*community, *tools, productionMapping())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "driftcheck: %v\n", err)
		return 2
	}
	printReport(stdout, *community, *tools, findings)
	if hasErrors(findings) {
		return 1
	}
	return 0
}

// productionMapping returns the name mapping from community struct names
// to their atlax-tools mirror struct(s). TLSPaths maps to two tools
// structs because tools splits relay and agent TLS configs.
func productionMapping() map[string][]string {
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

// runDriftCheck loads both files, extracts struct field metadata, and
// returns the findings list. err is returned only for IO or parse errors.
func runDriftCheck(communityPath, toolsPath string, mapping map[string][]string) ([]Finding, error) {
	if err := validatePath(communityPath); err != nil {
		return nil, fmt.Errorf("community path: %w", err)
	}
	if err := validatePath(toolsPath); err != nil {
		return nil, fmt.Errorf("tools path: %w", err)
	}

	community, err := parseStructs(communityPath)
	if err != nil {
		return nil, fmt.Errorf("parse community: %w", err)
	}
	tools, err := parseStructs(toolsPath)
	if err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	return compareMapped(community, tools, mapping), nil
}

// validatePath guards against obvious misuse: the path must exist and
// must have a .go extension. Clean the path so that ".." segments are
// resolved before any downstream consumer uses it.
func validatePath(p string) error {
	if p == "" {
		return errors.New("path is empty")
	}
	if filepath.Ext(p) != ".go" {
		return fmt.Errorf("expected .go file, got %q", p)
	}
	info, err := os.Stat(filepath.Clean(p))
	if err != nil {
		return fmt.Errorf("stat %q: %w", p, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory", p)
	}
	return nil
}

// parseStructs returns a map of struct name to its ordered YAML-tagged
// fields. Fields without a yaml tag are skipped. The "-" skip directive
// is respected. Embedded (anonymous) fields are skipped.
func parseStructs(path string) (map[string][]Field, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Clean(path), nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]Field)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			fields := extractFields(st)
			// Record every struct, even with zero yaml-tagged fields, so
			// the comparer can distinguish "struct missing" from "struct
			// present but empty".
			out[ts.Name.Name] = fields
		}
	}
	return out, nil
}

// extractFields walks a struct's field list and returns the ordered
// YAML-tagged fields. Grouped declarations (A, B string) yield one
// Field per name. Anonymous fields and fields without a yaml tag are
// skipped. Tag options after the first comma are stripped from YAMLName
// but preserved in Tag for diagnostic output.
func extractFields(st *ast.StructType) []Field {
	var out []Field
	if st.Fields == nil {
		return out
	}
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			// Embedded / anonymous field: skip.
			continue
		}
		tag := yamlTag(f.Tag)
		if tag == "" || strings.HasPrefix(tag, "-") {
			continue
		}
		yamlName := tag
		if i := strings.Index(tag, ","); i >= 0 {
			yamlName = tag[:i]
		}
		if yamlName == "" {
			continue
		}
		for _, n := range f.Names {
			out = append(out, Field{
				Name:     n.Name,
				YAMLName: yamlName,
				Tag:      tag,
			})
		}
	}
	return out
}

// yamlTag extracts the yaml tag contents from a struct tag literal, or
// empty string if absent. Handles the BasicLit `...` raw form that the
// parser stores struct tags in.
func yamlTag(lit *ast.BasicLit) string {
	if lit == nil {
		return ""
	}
	raw := lit.Value
	// Struct tags are stored as raw backtick strings; strip surrounding
	// backticks or double quotes the parser may hand us.
	if len(raw) >= 2 && (raw[0] == '`' || raw[0] == '"') {
		raw = raw[1 : len(raw)-1]
	}
	// Find yaml:"..." within the tag.
	// reflect.StructTag.Get is the canonical parser; rather than import
	// reflect for a trivial lookup, we split by spaces and look up the key.
	for _, part := range strings.Split(raw, " ") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "yaml:") {
			continue
		}
		v := strings.TrimPrefix(part, "yaml:")
		if len(v) < 2 || v[0] != '"' {
			return ""
		}
		// Trim surrounding double quotes from yaml:"...".
		v = v[1:]
		if end := strings.LastIndex(v, "\""); end >= 0 {
			v = v[:end]
		}
		return v
	}
	return ""
}

// compareMapped walks the mapping and records findings.
func compareMapped(community, tools map[string][]Field, mapping map[string][]string) []Finding {
	var findings []Finding

	// Deterministic order: sort community keys from the mapping.
	keys := make([]string, 0, len(mapping))
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, cName := range keys {
		cFields, cOK := community[cName]
		if !cOK {
			// Community source does not define this mapped struct.
			// This is not drift -- the mapping may simply predate an
			// upstream rename or removal. Skip silently. Maintainers
			// can prune stale mapping entries during review.
			continue
		}
		for _, tName := range mapping[cName] {
			tFields, tOK := tools[tName]
			if !tOK {
				findings = append(findings, Finding{
					Kind:            KindMissingStruct,
					CommunityStruct: cName,
					ToolsStruct:     tName,
					Detail:          "community struct has no tools mirror",
				})
				continue
			}
			findings = append(findings, compareFields(cName, tName, cFields, tFields)...)
		}
	}
	return findings
}

// compareFields produces findings for a single community/tools struct
// pair. It compares by Go identifier name (since the tools mirror uses
// the same identifiers as community for fields that exist in both), and
// flags YAML tag mismatches.
func compareFields(cStruct, tStruct string, c, t []Field) []Finding {
	var findings []Finding

	tIdx := make(map[string]Field, len(t))
	tByYAML := make(map[string]Field, len(t))
	for _, f := range t {
		tIdx[f.Name] = f
		tByYAML[f.YAMLName] = f
	}
	cIdx := make(map[string]Field, len(c))
	cByYAML := make(map[string]Field, len(c))
	for _, f := range c {
		cIdx[f.Name] = f
		cByYAML[f.YAMLName] = f
	}

	// MissingField and TagMismatch: iterate community, look up in tools.
	for _, cf := range c {
		tf, byName := tIdx[cf.Name]
		if !byName {
			// Try YAML-name fallback. This handles the case where the
			// tools struct uses a different Go identifier but the same
			// YAML tag (serialised compatibility preserved).
			if _, okYaml := tByYAML[cf.YAMLName]; okYaml {
				continue
			}
			findings = append(findings, Finding{
				Kind:            KindMissingField,
				CommunityStruct: cStruct,
				ToolsStruct:     tStruct,
				Field:           cf.YAMLName,
				CommunityTag:    cf.Tag,
				Detail:          "community field has no mirror in tools struct",
			})
			continue
		}
		if tf.YAMLName != cf.YAMLName {
			findings = append(findings, Finding{
				Kind:            KindTagMismatch,
				CommunityStruct: cStruct,
				ToolsStruct:     tStruct,
				Field:           cf.Name,
				CommunityTag:    cf.Tag,
				ToolsTag:        tf.Tag,
				Detail:          "yaml tag differs between community and tools",
			})
		}
	}

	// ExtraInTools: iterate tools, look up in community.
	for _, tf := range t {
		if _, byName := cIdx[tf.Name]; byName {
			continue
		}
		if _, byYAML := cByYAML[tf.YAMLName]; byYAML {
			continue
		}
		findings = append(findings, Finding{
			Kind:            KindExtraInTools,
			CommunityStruct: cStruct,
			ToolsStruct:     tStruct,
			Field:           tf.YAMLName,
			ToolsTag:        tf.Tag,
			Detail:          "tools has field not present in community (may be intentional)",
		})
	}
	return findings
}

// printReport writes a human-readable summary. Errors first, warnings
// after, with a trailing one-line summary that CI logs can grep.
func printReport(w io.Writer, communityPath, toolsPath string, findings []Finding) {
	out := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, format, args...)
	}
	out("driftcheck\n")
	out("  community: %s\n", communityPath)
	out("  tools:     %s\n", toolsPath)

	if len(findings) == 0 {
		out("result: OK (no drift detected)\n")
		return
	}

	sorted := make([]Finding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].severity() != sorted[j].severity() {
			// severityError (1) sorts before severityWarn (0).
			return sorted[i].severity() > sorted[j].severity()
		}
		if sorted[i].CommunityStruct != sorted[j].CommunityStruct {
			return sorted[i].CommunityStruct < sorted[j].CommunityStruct
		}
		return sorted[i].Field < sorted[j].Field
	})

	var errCount, warnCount int
	for _, f := range sorted {
		if f.severity() == severityError {
			errCount++
			out("  [ERROR] %s\n", formatFinding(f))
		} else {
			warnCount++
			out("  [WARN]  %s\n", formatFinding(f))
		}
	}
	if errCount > 0 {
		out("result: DRIFT (%d error, %d warning)\n", errCount, warnCount)
	} else {
		out("result: OK (%d warning, no errors)\n", warnCount)
	}
}

// formatFinding renders a single Finding to a single line.
func formatFinding(f Finding) string {
	switch f.Kind {
	case KindMissingStruct:
		if f.ToolsStruct == "" {
			return fmt.Sprintf("missing struct in community: %s", f.CommunityStruct)
		}
		return fmt.Sprintf("missing struct in tools: %s (community: %s)", f.ToolsStruct, f.CommunityStruct)
	case KindMissingField:
		return fmt.Sprintf("missing field in tools: %s.%s <- community: %s.%s (tag %q)",
			f.ToolsStruct, f.Field, f.CommunityStruct, f.Field, f.CommunityTag)
	case KindTagMismatch:
		return fmt.Sprintf("yaml tag mismatch: %s.%s community=%q tools=%q",
			f.ToolsStruct, f.Field, f.CommunityTag, f.ToolsTag)
	case KindExtraInTools:
		return fmt.Sprintf("tools-only field: %s.%s (tag %q, community %s)",
			f.ToolsStruct, f.Field, f.ToolsTag, f.CommunityStruct)
	default:
		return f.Detail
	}
}
