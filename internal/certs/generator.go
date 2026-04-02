// Package certs handles certificate generation using either OpenSSL or
// step-ca CLI, with auto-detection of available tools.
package certs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

// Backend represents which cert generation tool to use.
type Backend int

const (
	OpenSSL Backend = iota
	StepCLI
)

func (b Backend) String() string {
	if b == StepCLI {
		return "step-ca (Smallstep)"
	}
	return "OpenSSL"
}

// Opts holds certificate generation parameters.
type Opts struct {
	OutputDir    string
	Backend      Backend
	RootCADays   int
	InterDays    int
	LeafDays     int
	CustomerID   string
	RelayDomain  string
	RelaySANs    []string // Additional SANs for relay cert
	DryRun       bool
}

// DefaultOpts returns sensible defaults.
func DefaultOpts() Opts {
	return Opts{
		OutputDir:   "./certs",
		Backend:     OpenSSL,
		RootCADays:  3650,  // 10 years
		InterDays:   1095,  // 3 years
		LeafDays:    90,
		RelayDomain: "relay.atlax.local",
		RelaySANs:   []string{"localhost", "127.0.0.1"},
	}
}

// DetectBackend checks which cert tools are available and returns the best one.
func DetectBackend() Backend {
	if _, err := exec.LookPath("step"); err == nil {
		return StepCLI
	}
	return OpenSSL
}

// GenerateFullPKI creates the complete certificate hierarchy:
//
//	Root CA → Relay Intermediate CA → relay.crt
//	Root CA → Customer Intermediate CA → agent.crt
func GenerateFullPKI(opts Opts) error {
	if err := os.MkdirAll(opts.OutputDir, 0700); err != nil {
		return fmt.Errorf("cannot create cert directory: %w", err)
	}

	steps := []struct {
		name string
		fn   func(Opts) error
	}{
		{"Generate Root CA", generateRootCA},
		{"Generate Relay Intermediate CA", generateRelayCA},
		{"Generate Customer Intermediate CA", generateCustomerCA},
		{"Generate Relay Certificate", generateRelayCert},
		{"Generate Agent Certificate", generateAgentCert},
		{"Build Certificate Chains", buildChains},
	}

	total := len(steps)
	for i, step := range steps {
		tui.Step(i+1, total, step.name)
		if opts.DryRun {
			tui.DryRunf("Would execute: %s", step.name)
			logger.Log(step.name, "dry-run skipped")
			continue
		}
		if err := step.fn(opts); err != nil {
			logger.LogError(step.name, "failed", err)
			return fmt.Errorf("%s failed: %w", step.name, err)
		}
		logger.Log(step.name, "completed")
		tui.Successf("%s", step.name)
	}

	return nil
}

// IssueAgentCert generates a new agent cert signed by the customer CA.
// Requires existing customer-ca.crt and customer-ca.key.
func IssueAgentCert(opts Opts) error {
	caKey := filepath.Join(opts.OutputDir, "customer-ca.key")
	caCert := filepath.Join(opts.OutputDir, "customer-ca.crt")

	if _, err := os.Stat(caKey); err != nil {
		return fmt.Errorf("customer CA key not found at %s: %w", caKey, err)
	}
	if _, err := os.Stat(caCert); err != nil {
		return fmt.Errorf("customer CA cert not found at %s: %w", caCert, err)
	}

	tui.Step(1, 2, "Generate agent key and CSR")
	if opts.DryRun {
		tui.DryRunf("Would generate agent cert for %s", opts.CustomerID)
		return nil
	}

	if err := generateAgentCert(opts); err != nil {
		return err
	}

	tui.Step(2, 2, "Build agent chain")
	return buildAgentChain(opts)
}

// --- OpenSSL implementations ---

func generateRootCA(opts Opts) error {
	keyPath := filepath.Join(opts.OutputDir, "root-ca.key")
	certPath := filepath.Join(opts.OutputDir, "root-ca.crt")

	if opts.Backend == StepCLI {
		return runCmd("step", "certificate", "create",
			"Atlax Root CA", certPath, keyPath,
			"--profile", "root-ca",
			"--not-after", fmt.Sprintf("%dh", opts.RootCADays*24),
			"--no-password", "--insecure",
			"--force")
	}

	// OpenSSL
	subj := "/C=US/O=Atlax/CN=Atlax Root CA"
	return runCmd("openssl", "req", "-x509", "-new", "-nodes",
		"-newkey", "rsa:4096",
		"-keyout", keyPath,
		"-out", certPath,
		"-days", fmt.Sprintf("%d", opts.RootCADays),
		"-subj", subj)
}

func generateRelayCA(opts Opts) error {
	keyPath := filepath.Join(opts.OutputDir, "relay-ca.key")
	csrPath := filepath.Join(opts.OutputDir, "relay-ca.csr")
	certPath := filepath.Join(opts.OutputDir, "relay-ca.crt")
	rootKey := filepath.Join(opts.OutputDir, "root-ca.key")
	rootCert := filepath.Join(opts.OutputDir, "root-ca.crt")

	if opts.Backend == StepCLI {
		return runCmd("step", "certificate", "create",
			"Atlax Relay CA", certPath, keyPath,
			"--profile", "intermediate-ca",
			"--ca", rootCert, "--ca-key", rootKey,
			"--not-after", fmt.Sprintf("%dh", opts.InterDays*24),
			"--no-password", "--insecure",
			"--force")
	}

	subj := "/C=US/O=Atlax/CN=Atlax Relay CA"
	extFile := filepath.Join(opts.OutputDir, "relay-ca-ext.cnf")
	if err := os.WriteFile(extFile, []byte(`[v3_ca]
basicConstraints = critical,CA:TRUE,pathlen:0
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
`), 0644); err != nil {
		return err
	}

	if err := runCmd("openssl", "req", "-new", "-nodes",
		"-newkey", "rsa:4096",
		"-keyout", keyPath,
		"-out", csrPath,
		"-subj", subj); err != nil {
		return err
	}

	return runCmd("openssl", "x509", "-req",
		"-in", csrPath,
		"-CA", rootCert, "-CAkey", rootKey,
		"-CAcreateserial",
		"-out", certPath,
		"-days", fmt.Sprintf("%d", opts.InterDays),
		"-extfile", extFile, "-extensions", "v3_ca")
}

func generateCustomerCA(opts Opts) error {
	keyPath := filepath.Join(opts.OutputDir, "customer-ca.key")
	csrPath := filepath.Join(opts.OutputDir, "customer-ca.csr")
	certPath := filepath.Join(opts.OutputDir, "customer-ca.crt")
	rootKey := filepath.Join(opts.OutputDir, "root-ca.key")
	rootCert := filepath.Join(opts.OutputDir, "root-ca.crt")

	if opts.Backend == StepCLI {
		return runCmd("step", "certificate", "create",
			"Atlax Customer CA", certPath, keyPath,
			"--profile", "intermediate-ca",
			"--ca", rootCert, "--ca-key", rootKey,
			"--not-after", fmt.Sprintf("%dh", opts.InterDays*24),
			"--no-password", "--insecure",
			"--force")
	}

	subj := "/C=US/O=Atlax/CN=Atlax Customer CA"
	extFile := filepath.Join(opts.OutputDir, "customer-ca-ext.cnf")
	if err := os.WriteFile(extFile, []byte(`[v3_ca]
basicConstraints = critical,CA:TRUE,pathlen:0
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
`), 0644); err != nil {
		return err
	}

	if err := runCmd("openssl", "req", "-new", "-nodes",
		"-newkey", "rsa:4096",
		"-keyout", keyPath,
		"-out", csrPath,
		"-subj", subj); err != nil {
		return err
	}

	return runCmd("openssl", "x509", "-req",
		"-in", csrPath,
		"-CA", rootCert, "-CAkey", rootKey,
		"-CAcreateserial",
		"-out", certPath,
		"-days", fmt.Sprintf("%d", opts.InterDays),
		"-extfile", extFile, "-extensions", "v3_ca")
}

func generateRelayCert(opts Opts) error {
	keyPath := filepath.Join(opts.OutputDir, "relay.key")
	csrPath := filepath.Join(opts.OutputDir, "relay.csr")
	certPath := filepath.Join(opts.OutputDir, "relay.crt")
	caKey := filepath.Join(opts.OutputDir, "relay-ca.key")
	caCert := filepath.Join(opts.OutputDir, "relay-ca.crt")

	// Build SAN list
	sans := []string{"DNS:" + opts.RelayDomain}
	for _, s := range opts.RelaySANs {
		if strings.Contains(s, ":") || isIPAddress(s) {
			sans = append(sans, "IP:"+s)
		} else {
			sans = append(sans, "DNS:"+s)
		}
	}

	if opts.Backend == StepCLI {
		args := []string{"certificate", "create",
			opts.RelayDomain, certPath, keyPath,
			"--profile", "leaf",
			"--ca", caCert, "--ca-key", caKey,
			"--not-after", fmt.Sprintf("%dh", opts.LeafDays*24),
			"--no-password", "--insecure",
			"--force",
		}
		for _, s := range opts.RelaySANs {
			args = append(args, "--san", s)
		}
		return runCmd("step", args...)
	}

	subj := fmt.Sprintf("/C=US/O=Atlax/CN=%s", opts.RelayDomain)
	extFile := filepath.Join(opts.OutputDir, "relay-ext.cnf")
	extContent := fmt.Sprintf(`[v3_req]
basicConstraints = CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = %s
`, strings.Join(sans, ","))

	if err := os.WriteFile(extFile, []byte(extContent), 0644); err != nil {
		return err
	}

	if err := runCmd("openssl", "req", "-new", "-nodes",
		"-newkey", "rsa:2048",
		"-keyout", keyPath,
		"-out", csrPath,
		"-subj", subj); err != nil {
		return err
	}

	return runCmd("openssl", "x509", "-req",
		"-in", csrPath,
		"-CA", caCert, "-CAkey", caKey,
		"-CAcreateserial",
		"-out", certPath,
		"-days", fmt.Sprintf("%d", opts.LeafDays),
		"-extfile", extFile, "-extensions", "v3_req")
}

func generateAgentCert(opts Opts) error {
	customerID := opts.CustomerID
	if customerID == "" {
		customerID = "customer-dev-001"
	}

	keyPath := filepath.Join(opts.OutputDir, "agent.key")
	csrPath := filepath.Join(opts.OutputDir, "agent.csr")
	certPath := filepath.Join(opts.OutputDir, "agent.crt")
	caKey := filepath.Join(opts.OutputDir, "customer-ca.key")
	caCert := filepath.Join(opts.OutputDir, "customer-ca.crt")

	if opts.Backend == StepCLI {
		return runCmd("step", "certificate", "create",
			customerID, certPath, keyPath,
			"--profile", "leaf",
			"--ca", caCert, "--ca-key", caKey,
			"--not-after", fmt.Sprintf("%dh", opts.LeafDays*24),
			"--no-password", "--insecure",
			"--force")
	}

	subj := fmt.Sprintf("/C=US/O=Atlax/CN=%s", customerID)
	extFile := filepath.Join(opts.OutputDir, "agent-ext.cnf")
	extContent := `[v3_req]
basicConstraints = CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = clientAuth
`
	if err := os.WriteFile(extFile, []byte(extContent), 0644); err != nil {
		return err
	}

	if err := runCmd("openssl", "req", "-new", "-nodes",
		"-newkey", "rsa:2048",
		"-keyout", keyPath,
		"-out", csrPath,
		"-subj", subj); err != nil {
		return err
	}

	return runCmd("openssl", "x509", "-req",
		"-in", csrPath,
		"-CA", caCert, "-CAkey", caKey,
		"-CAcreateserial",
		"-out", certPath,
		"-days", fmt.Sprintf("%d", opts.LeafDays),
		"-extfile", extFile, "-extensions", "v3_req")
}

func buildChains(opts Opts) error {
	dir := opts.OutputDir

	// Relay chain: relay.crt + relay-ca.crt
	if err := concatFiles(
		filepath.Join(dir, "relay-chain.crt"),
		filepath.Join(dir, "relay.crt"),
		filepath.Join(dir, "relay-ca.crt"),
	); err != nil {
		return fmt.Errorf("relay chain: %w", err)
	}

	// Agent chain: agent.crt + customer-ca.crt
	if err := buildAgentChain(opts); err != nil {
		return err
	}

	// Intermediate CAs bundle
	return concatFiles(
		filepath.Join(dir, "intermediate-cas.crt"),
		filepath.Join(dir, "relay-ca.crt"),
		filepath.Join(dir, "customer-ca.crt"),
	)
}

func buildAgentChain(opts Opts) error {
	dir := opts.OutputDir
	return concatFiles(
		filepath.Join(dir, "agent-chain.crt"),
		filepath.Join(dir, "agent.crt"),
		filepath.Join(dir, "customer-ca.crt"),
	)
}

// --- helpers ---

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %s failed: %w", name, err)
	}
	return nil
}

func concatFiles(output string, inputs ...string) error {
	var combined []byte
	for _, in := range inputs {
		data, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		combined = append(combined, data...)
	}
	return os.WriteFile(output, combined, 0644)
}

func isIPAddress(s string) bool {
	for _, c := range s {
		if c != '.' && (c < '0' || c > '9') && c != ':' {
			return false
		}
	}
	return true
}
