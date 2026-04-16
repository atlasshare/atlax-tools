package certs

import (
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- PATH / LibreSSL shim -----------------------------------------------
//
// On macOS, /usr/bin/openssl is LibreSSL, which has a known bug in the
// combination of `-CAcreateserial` and the v3 extension
// `authorityKeyIdentifier=keyid:always,issuer` used by this package.
// If a real OpenSSL (>= 1.1) is available via Homebrew or on PATH, we
// prepend it. Otherwise these integration-style tests are skipped.
//
// This is strictly a test-environment adjustment — production hosts are
// Linux and ship real OpenSSL.

var (
	sharedPKIOnce sync.Once
	sharedPKIDir  string
	sharedPKIErr  error
)

// ensureRealOpenSSL returns a PATH value that places a non-LibreSSL
// openssl first, or the empty string if none is available.
func ensureRealOpenSSL(t *testing.T) string {
	t.Helper()
	// 1. If the current openssl on PATH is already real OpenSSL, no-op.
	if isGNUOpenSSL(t, "openssl") {
		return os.Getenv("PATH")
	}

	// 2. Probe common Homebrew prefixes on macOS.
	if runtime.GOOS == "darwin" {
		for _, prefix := range []string{
			"/opt/homebrew/opt/openssl@3/bin",
			"/usr/local/opt/openssl@3/bin",
			"/opt/homebrew/opt/openssl@1.1/bin",
			"/usr/local/opt/openssl@1.1/bin",
		} {
			candidate := filepath.Join(prefix, "openssl")
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			if isGNUOpenSSL(t, candidate) {
				return prefix + string(os.PathListSeparator) + os.Getenv("PATH")
			}
		}
	}
	return ""
}

func isGNUOpenSSL(t *testing.T, bin string) bool {
	t.Helper()
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		return false
	}
	return !strings.Contains(string(out), "LibreSSL")
}

// sharedPKI returns the path to a directory containing a full PKI
// generated once for the test binary. Skips the calling test if no
// real OpenSSL is available.
func sharedPKI(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping OpenSSL-based PKI tests in -short mode")
	}

	sharedPKIOnce.Do(func() {
		newPath := ensureRealOpenSSL(t)
		if newPath == "" {
			sharedPKIErr = errSkip
			return
		}
		// Persist for the life of the test binary.
		_ = os.Setenv("PATH", newPath)

		dir, err := os.MkdirTemp("", "atlax-certs-shared-")
		if err != nil {
			sharedPKIErr = err
			return
		}
		// Smaller keys keep the full run under a few seconds while still
		// producing genuine x509 PEM output. We do this by overriding
		// OpenSSL config via env; however the shelled-out commands hard
		// code rsa:4096 so we just accept the cost — but only once for
		// the whole binary.
		opts := DefaultOpts()
		opts.OutputDir = dir
		opts.Backend = OpenSSL
		if err := GenerateFullPKI(opts); err != nil {
			sharedPKIErr = err
			return
		}
		sharedPKIDir = dir
	})

	if sharedPKIErr == errSkip {
		t.Skip("no real OpenSSL available; skipping PKI generation tests")
	}
	if sharedPKIErr != nil {
		t.Fatalf("shared PKI setup: %v", sharedPKIErr)
	}
	return sharedPKIDir
}

// errSkip is a sentinel used only inside this test file to distinguish
// "skip" from "setup failure" in a sync.Once-captured error.
var errSkip = skipErr{}

type skipErr struct{}

func (skipErr) Error() string { return "skip" }

// --- helpers -------------------------------------------------------------

func parseLeafPEM(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)

	block, _ := pem.Decode(data)
	require.NotNil(t, block, "PEM decode failed for %s", path)
	require.Equal(t, "CERTIFICATE", block.Type, "expected CERTIFICATE block in %s", path)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err, "x509 parse failed for %s", path)
	return cert
}

func parsePrivateKeyPEM(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	block, _ := pem.Decode(data)
	require.NotNil(t, block, "PEM decode failed for key %s", path)
	assert.Contains(t, block.Type, "PRIVATE KEY",
		"expected PRIVATE KEY in %s, got %q", path, block.Type)
}

// daysApart returns the absolute difference between two times in days.
func daysApart(a, b time.Time) float64 {
	d := a.Sub(b).Hours() / 24
	if d < 0 {
		d = -d
	}
	return d
}

// --- Pure-Go tests (no shell-out) ---------------------------------------

func TestDefaultOpts(t *testing.T) {
	t.Parallel()
	opts := DefaultOpts()
	assert.Equal(t, "./certs", opts.OutputDir)
	assert.Equal(t, OpenSSL, opts.Backend)
	assert.Equal(t, 3650, opts.RootCADays, "root CA default should be 10 years")
	assert.Equal(t, 1095, opts.InterDays, "intermediate default should be 3 years")
	assert.Equal(t, 90, opts.LeafDays, "leaf default should be 90 days")
	assert.Equal(t, "relay.atlax.local", opts.RelayDomain)
	assert.ElementsMatch(t, []string{"localhost", "127.0.0.1"}, opts.RelaySANs)
}

func TestBackend_String(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "OpenSSL", OpenSSL.String())
	assert.Equal(t, "step-ca (Smallstep)", StepCLI.String())
	// Unknown backend value falls through to the OpenSSL branch.
	assert.Equal(t, "OpenSSL", Backend(42).String())
}

func TestDetectBackend(t *testing.T) {
	// Not parallel: mutates PATH via t.Setenv.
	// DetectBackend consults PATH. Neutralising PATH must fall back to
	// OpenSSL because `step` cannot be found.
	t.Setenv("PATH", "")
	assert.Equal(t, OpenSSL, DetectBackend())
}

func TestIsIPAddress(t *testing.T) {
	t.Parallel()
	// isIPAddress is a cheap structural check used only to decide whether
	// a SAN string should be prefixed with "IP:" or "DNS:". It accepts
	// anything composed of digits, dots, and colons (i.e. plausible IPv4
	// decimal or numeric IPv6 portions). It does not parse hex-letter
	// IPv6 segments — that is out of scope for its current callers.
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.255", true},
		{"::1", true},
		{"1.2.3.4", true},
		{"localhost", false},
		{"example.com", false},
		{"fe80::1", false}, // letters trip the check by design.
		{"mixed.1.2.3", false},
		{"", true}, // empty string has no non-digit/dot/colon runes.
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, isIPAddress(tc.in),
			"isIPAddress(%q)", tc.in)
	}
}

func TestConcatFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	out := filepath.Join(dir, "out.txt")

	require.NoError(t, os.WriteFile(a, []byte("alpha\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("beta\n"), 0o644))

	require.NoError(t, concatFiles(out, a, b))

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "alpha\nbeta\n", string(data))
}

func TestConcatFiles_MissingInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	err := concatFiles(out, filepath.Join(dir, "does-not-exist"))
	require.Error(t, err)
}

func TestRunCmd_UnknownBinary(t *testing.T) {
	t.Parallel()
	err := runCmd("ats-totally-not-a-real-binary-xyz", "--noop")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ats-totally-not-a-real-binary-xyz")
}

// --- Dry-run tests ------------------------------------------------------

func TestGenerateFullPKI_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := DefaultOpts()
	opts.OutputDir = dir
	opts.DryRun = true

	require.NoError(t, GenerateFullPKI(opts))

	// Only the output directory should exist — no cert files.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "dry-run must not produce any files")
}

func TestGenerateFullPKI_CreatesOutputDirectory(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	target := filepath.Join(parent, "new-certs-dir")

	opts := DefaultOpts()
	opts.OutputDir = target
	opts.DryRun = true

	require.NoError(t, GenerateFullPKI(opts))

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	// 0700 per security convention.
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestIssueAgentCert_MissingCAKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Write only the cert file — no key.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "customer-ca.crt"), []byte("dummy"), 0o600))

	opts := DefaultOpts()
	opts.OutputDir = dir
	err := IssueAgentCert(opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "customer CA key not found")
}

func TestIssueAgentCert_MissingCACert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "customer-ca.key"), []byte("dummy"), 0o600))

	opts := DefaultOpts()
	opts.OutputDir = dir
	err := IssueAgentCert(opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "customer CA cert not found")
}

func TestIssueAgentCert_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Existing stub CA files to satisfy the existence check.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "customer-ca.key"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "customer-ca.crt"), []byte("x"), 0o600))

	opts := DefaultOpts()
	opts.OutputDir = dir
	opts.CustomerID = "customer-dry"
	opts.DryRun = true

	require.NoError(t, IssueAgentCert(opts))
	// Dry-run must not create agent cert files.
	for _, name := range []string{"agent.crt", "agent.key", "agent-chain.crt"} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.True(t, os.IsNotExist(err), "%s must not be created in dry run", name)
	}
}

// --- Real cert-generation tests (shell out to OpenSSL) ------------------

func TestGenerateFullPKI_RealOutputs(t *testing.T) {
	dir := sharedPKI(t)

	expectedFiles := []string{
		"root-ca.crt", "root-ca.key",
		"relay-ca.crt", "relay-ca.key",
		"customer-ca.crt", "customer-ca.key",
		"relay.crt", "relay.key",
		"agent.crt", "agent.key",
		"relay-chain.crt", "agent-chain.crt",
		"intermediate-cas.crt",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(dir, f)
		info, err := os.Stat(path)
		require.NoErrorf(t, err, "expected file %s to exist", f)
		assert.NotZero(t, info.Size(), "file %s should be non-empty", f)
	}
}

func TestGeneratedPrivateKeys_ArePEM(t *testing.T) {
	dir := sharedPKI(t)
	for _, f := range []string{
		"root-ca.key", "relay-ca.key", "customer-ca.key",
		"relay.key", "agent.key",
	} {
		parsePrivateKeyPEM(t, filepath.Join(dir, f))
	}
}

// TestGeneratedPrivateKeys_RestrictivePerms verifies that key files are
// not group- or world-accessible. The project security convention (per
// workspace CLAUDE.md) is 0600 for key files. OpenSSL >= 1.1 emits keys
// at 0600 by default on POSIX systems, and modern LibreSSL does the
// same. We assert the *minimum* invariant (no `go-other` bits) so the
// test does not regress on unusual umask environments while still
// catching any accidental widening of the default.
func TestGeneratedPrivateKeys_RestrictivePerms(t *testing.T) {
	dir := sharedPKI(t)
	for _, f := range []string{
		"root-ca.key", "relay-ca.key", "customer-ca.key",
		"relay.key", "agent.key",
	} {
		info, err := os.Stat(filepath.Join(dir, f))
		require.NoErrorf(t, err, "stat %s", f)
		perm := info.Mode().Perm()

		// No bits for group or other: 0o077 == (group rwx | other rwx).
		assert.Zerof(t, perm&0o077,
			"key file %s must not be accessible by group or other (got %04o)", f, perm)
	}
}

func TestRootCA_ExpirySerialAndCA(t *testing.T) {
	dir := sharedPKI(t)
	cert := parseLeafPEM(t, filepath.Join(dir, "root-ca.crt"))

	assert.True(t, cert.IsCA, "root cert must be a CA")

	// Serial must be non-zero.
	assert.Equal(t, 1, cert.SerialNumber.Cmp(big.NewInt(0)),
		"serial should be > 0, got %s", cert.SerialNumber.String())

	// 10 years ± 2 days tolerance.
	wantDays := 3650.0
	got := daysApart(cert.NotAfter, cert.NotBefore)
	assert.InDeltaf(t, wantDays, got, 2.0,
		"root CA validity should be ~%.0f days, got %.1f", wantDays, got)

	// Subject CN.
	assert.Contains(t, cert.Subject.CommonName, "Root CA")
}

func TestIntermediateCAs_ExpiryAndCA(t *testing.T) {
	dir := sharedPKI(t)

	for name, wantCN := range map[string]string{
		"relay-ca.crt":    "Relay CA",
		"customer-ca.crt": "Customer CA",
	} {
		cert := parseLeafPEM(t, filepath.Join(dir, name))
		assert.Truef(t, cert.IsCA, "%s must be a CA", name)
		assert.Equalf(t, 1, cert.SerialNumber.Cmp(big.NewInt(0)),
			"%s serial should be > 0", name)
		got := daysApart(cert.NotAfter, cert.NotBefore)
		assert.InDeltaf(t, 1095.0, got, 2.0,
			"%s validity should be ~1095 days, got %.1f", name, got)
		assert.Containsf(t, cert.Subject.CommonName, wantCN,
			"%s subject CN should mention %q", name, wantCN)
	}
}

func TestLeafCerts_RelayAndAgent(t *testing.T) {
	dir := sharedPKI(t)

	relay := parseLeafPEM(t, filepath.Join(dir, "relay.crt"))
	assert.False(t, relay.IsCA, "relay cert must not be a CA")
	assert.Equal(t, 1, relay.SerialNumber.Cmp(big.NewInt(0)))
	relayDays := daysApart(relay.NotAfter, relay.NotBefore)
	assert.InDelta(t, 90.0, relayDays, 2.0, "relay leaf validity should be ~90 days")
	// Relay cert subject CN should be relay domain per DefaultOpts.
	assert.Contains(t, relay.Subject.CommonName, "relay.atlax.local")
	// Must carry serverAuth EKU.
	hasServerAuth := false
	for _, eku := range relay.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	assert.True(t, hasServerAuth, "relay leaf must carry serverAuth EKU")
	// SANs.
	assert.Contains(t, relay.DNSNames, "relay.atlax.local")

	agent := parseLeafPEM(t, filepath.Join(dir, "agent.crt"))
	assert.False(t, agent.IsCA, "agent cert must not be a CA")
	agentDays := daysApart(agent.NotAfter, agent.NotBefore)
	assert.InDelta(t, 90.0, agentDays, 2.0, "agent leaf validity should be ~90 days")
	// Default CustomerID path ("customer-dev-001").
	assert.Contains(t, agent.Subject.CommonName, "customer-dev-001")
	// Must carry clientAuth EKU.
	hasClientAuth := false
	for _, eku := range agent.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	assert.True(t, hasClientAuth, "agent leaf must carry clientAuth EKU")
}

func TestChains_ConcatenatedInOrder(t *testing.T) {
	dir := sharedPKI(t)

	chainData, err := os.ReadFile(filepath.Join(dir, "relay-chain.crt"))
	require.NoError(t, err)
	// Expect two PEM CERTIFICATE blocks: leaf then intermediate.
	blocks := decodeAllPEM(t, chainData)
	require.GreaterOrEqual(t, len(blocks), 2)
	// First block = leaf, second block = intermediate.
	leaf, err := x509.ParseCertificate(blocks[0].Bytes)
	require.NoError(t, err)
	assert.False(t, leaf.IsCA, "first block in relay-chain should be the leaf")

	inter, err := x509.ParseCertificate(blocks[1].Bytes)
	require.NoError(t, err)
	assert.True(t, inter.IsCA, "second block should be the relay CA")

	agentData, err := os.ReadFile(filepath.Join(dir, "agent-chain.crt"))
	require.NoError(t, err)
	agentBlocks := decodeAllPEM(t, agentData)
	require.GreaterOrEqual(t, len(agentBlocks), 2)
}

func decodeAllPEM(t *testing.T, data []byte) []*pem.Block {
	t.Helper()
	var blocks []*pem.Block
	rest := data
	for {
		block, r := pem.Decode(rest)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		rest = r
	}
	return blocks
}

func TestIssueAgentCert_ReusesExistingCA(t *testing.T) {
	// Use the shared PKI as the starting point.
	dir := sharedPKI(t)

	// Snapshot existing CA file mtimes and content to verify reuse.
	caKeyBefore := mustReadFile(t, filepath.Join(dir, "customer-ca.key"))
	caCrtBefore := mustReadFile(t, filepath.Join(dir, "customer-ca.crt"))

	// Copy CA into a new temp dir to avoid mutating the shared one's agent files.
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "customer-ca.key"), caKeyBefore, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "customer-ca.crt"), caCrtBefore, 0o600))

	opts := DefaultOpts()
	opts.OutputDir = workDir
	opts.CustomerID = "customer-test-007"
	require.NoError(t, IssueAgentCert(opts))

	// Agent cert + chain should now exist.
	for _, f := range []string{"agent.crt", "agent.key", "agent-chain.crt"} {
		_, err := os.Stat(filepath.Join(workDir, f))
		require.NoErrorf(t, err, "IssueAgentCert should produce %s", f)
	}

	// Customer CA files must be byte-identical after issuance (no regeneration).
	assert.Equal(t, caKeyBefore, mustReadFile(t, filepath.Join(workDir, "customer-ca.key")))
	assert.Equal(t, caCrtBefore, mustReadFile(t, filepath.Join(workDir, "customer-ca.crt")))

	// Agent CN must carry the requested customer ID.
	agent := parseLeafPEM(t, filepath.Join(workDir, "agent.crt"))
	assert.Contains(t, agent.Subject.CommonName, "customer-test-007")
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	return data
}
