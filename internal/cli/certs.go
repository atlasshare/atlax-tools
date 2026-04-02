package cli

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/certs"
	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newCertsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certs",
		Short: "Certificate management — generate, issue, rotate, inspect",
	}
	cmd.AddCommand(
		newCertsInitCmd(),
		newCertsIssueCmd(),
		newCertsRotateCmd(),
		newCertsInspectCmd(),
	)
	return cmd
}

// ---------- certs init ----------

func newCertsInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Generate full PKI hierarchy (Root CA → Intermediate CAs → leaf certs)",
		RunE:  runCertsInit,
	}
}

func runCertsInit(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Certificate PKI Initialization")

	outputDir := tui.Ask("Output directory", "./certs")

	// Check existing
	if _, err := os.Stat(filepath.Join(outputDir, "root-ca.crt")); err == nil {
		if !tui.Confirm("Existing certs found. Overwrite?", false) {
			tui.Warn("Aborted.")
			return nil
		}
	}

	// Backend
	backend := certs.DetectBackend()
	if platInfo.HasStepCLI && platInfo.HasOpenSSL {
		idx := tui.Select("Certificate backend", []string{"OpenSSL", "step-ca (Smallstep)"}, int(backend))
		backend = certs.Backend(idx)
	}
	tui.Infof("Using %s", backend)

	// Parameters
	relayDomain := tui.Ask("Relay domain (CN for relay cert)", "relay.atlax.local")
	extraSANs := tui.Ask("Additional relay SANs (comma-separated)", "localhost,127.0.0.1")
	customerID := tui.Ask("Initial agent customer ID", "customer-dev-001")

	rootDays := tui.AskInt("Root CA validity (days)", 3650)
	interDays := tui.AskInt("Intermediate CA validity (days)", 1095)
	leafDays := tui.AskInt("Leaf cert validity (days)", 90)

	opts := certs.Opts{
		OutputDir:   outputDir,
		Backend:     backend,
		RootCADays:  rootDays,
		InterDays:   interDays,
		LeafDays:    leafDays,
		CustomerID:  customerID,
		RelayDomain: relayDomain,
		DryRun:      dryRun,
	}
	for _, s := range splitAndTrim(extraSANs) {
		opts.RelaySANs = append(opts.RelaySANs, s)
	}

	tui.Header("Generating PKI")
	if err := certs.GenerateFullPKI(opts); err != nil {
		return err
	}

	tui.Header("Certificate Files")
	files := []string{
		"root-ca.crt", "root-ca.key",
		"relay-ca.crt", "relay-ca.key",
		"customer-ca.crt", "customer-ca.key",
		"relay.crt", "relay.key",
		"agent.crt", "agent.key",
		"relay-chain.crt", "agent-chain.crt",
		"intermediate-cas.crt",
	}
	for _, f := range files {
		path := filepath.Join(outputDir, f)
		if _, err := os.Stat(path); err == nil {
			tui.Successf("%s", f)
		} else {
			tui.Warnf("%s (missing)", f)
		}
	}

	tui.Header("Next Steps")
	tui.Infof("Copy relay certs to relay node: relay.crt, relay.key, root-ca.crt, customer-ca.crt")
	tui.Infof("Copy agent certs to agent node: agent.crt, agent.key, relay-ca.crt")
	tui.Infof("Store root-ca.key OFFLINE — it signs intermediate CAs")

	return nil
}

// ---------- certs issue ----------

func newCertsIssueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "issue",
		Short: "Issue a new agent certificate for a customer",
		RunE:  runCertsIssue,
	}
}

func runCertsIssue(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Issue Agent Certificate")

	certDir := tui.Ask("Certificate directory (with customer-ca.crt/key)", "./certs")
	prefix := tui.Ask("Customer ID prefix", "customer")
	suffix := tui.AskRequired("Customer ID suffix (e.g., prod-001 or UUID)")
	customerID := fmt.Sprintf("%s-%s", prefix, suffix)
	leafDays := tui.AskInt("Validity (days)", 90)

	backend := certs.DetectBackend()
	if platInfo.HasStepCLI && platInfo.HasOpenSSL {
		idx := tui.Select("Backend", []string{"OpenSSL", "step-ca"}, int(backend))
		backend = certs.Backend(idx)
	}

	// Output to a subdirectory named after the customer.
	outputDir := tui.Ask("Output directory", filepath.Join(certDir, customerID))

	opts := certs.Opts{
		OutputDir:  certDir, // CA files are here
		Backend:    backend,
		LeafDays:   leafDays,
		CustomerID: customerID,
		DryRun:     dryRun,
	}

	tui.Infof("Issuing cert for %s (valid %d days)", customerID, leafDays)
	if err := certs.IssueAgentCert(opts); err != nil {
		return err
	}

	// Move agent.crt/key to customer-specific directory.
	if !dryRun && outputDir != certDir {
		if err := os.MkdirAll(outputDir, 0700); err != nil {
			return err
		}
		for _, f := range []string{"agent.crt", "agent.key", "agent-chain.crt"} {
			src := filepath.Join(certDir, f)
			dst := filepath.Join(outputDir, f)
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			perm := os.FileMode(0644)
			if f == "agent.key" {
				perm = 0600
			}
			_ = os.WriteFile(dst, data, perm)
		}
		tui.Successf("Certs written to %s", outputDir)
	}

	logger.Log("issue-cert", customerID)
	return nil
}

// ---------- certs rotate ----------

func newCertsRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate",
		Short: "Check and rotate expiring certificates",
		RunE:  runCertsRotate,
	}
}

func runCertsRotate(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Certificate Rotation Check")

	certDir := tui.Ask("Certificate directory", "/etc/atlax/certs")
	thresholdDays := tui.AskInt("Renewal threshold (days before expiry)", 30)

	files := []string{"relay.crt", "agent.crt", "relay-ca.crt", "customer-ca.crt", "root-ca.crt"}
	var needsRotation []string

	for _, f := range files {
		path := filepath.Join(certDir, f)
		data, err := os.ReadFile(path)
		if err != nil {
			tui.Warnf("%-20s  not found", f)
			continue
		}

		block, _ := pem.Decode(data)
		if block == nil {
			tui.Warnf("%-20s  invalid PEM", f)
			continue
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			tui.Warnf("%-20s  cannot parse: %s", f, err)
			continue
		}

		daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
		subject := cert.Subject.CommonName

		if daysLeft <= 0 {
			tui.Failf("%-20s  EXPIRED (%s, CN=%s)", f, cert.NotAfter.Format("2006-01-02"), subject)
			needsRotation = append(needsRotation, f)
		} else if daysLeft <= thresholdDays {
			tui.Warnf("%-20s  expires in %d days (%s, CN=%s)", f, daysLeft, cert.NotAfter.Format("2006-01-02"), subject)
			needsRotation = append(needsRotation, f)
		} else {
			tui.Successf("%-20s  %d days remaining (CN=%s)", f, daysLeft, subject)
		}
	}

	if len(needsRotation) == 0 {
		tui.Header("All certificates are healthy")
		return nil
	}

	tui.Header("Certificates Needing Rotation")
	for _, f := range needsRotation {
		tui.Infof("  %s", f)
	}

	if !tui.Confirm("Regenerate expiring leaf certs now?", true) {
		return nil
	}

	backend := certs.DetectBackend()

	for _, f := range needsRotation {
		switch f {
		case "relay.crt":
			tui.Infof("Regenerating relay certificate...")
			relayDomain := tui.Ask("Relay domain", "relay.atlax.local")
			opts := certs.Opts{
				OutputDir:   certDir,
				Backend:     backend,
				LeafDays:    90,
				RelayDomain: relayDomain,
				RelaySANs:   []string{"localhost", "127.0.0.1"},
				DryRun:      dryRun,
			}
			// Backup first
			if !dryRun {
				if bak, err := backupCert(certDir, f); err == nil {
					tui.Successf("Backed up to %s", bak)
				}
			}
			if err := certs.GenerateFullPKI(opts); err != nil {
				tui.Failf("Failed to regenerate relay cert: %s", err)
			} else {
				tui.Successf("Relay certificate rotated")
				logger.Log("rotate-cert", "relay.crt")
			}

		case "agent.crt":
			tui.Infof("Regenerating agent certificate...")
			customerID := tui.AskRequired("Customer ID for this agent cert")
			opts := certs.Opts{
				OutputDir:  certDir,
				Backend:    backend,
				LeafDays:   90,
				CustomerID: customerID,
				DryRun:     dryRun,
			}
			if !dryRun {
				if bak, err := backupCert(certDir, f); err == nil {
					tui.Successf("Backed up to %s", bak)
				}
			}
			if err := certs.IssueAgentCert(opts); err != nil {
				tui.Failf("Failed to regenerate agent cert: %s", err)
			} else {
				tui.Successf("Agent certificate rotated")
				logger.Log("rotate-cert", "agent.crt")
			}

		default:
			tui.Warnf("CA certificate %s is expiring — this requires manual renewal with the root CA key", f)
		}
	}

	tui.Header("Post-Rotation Steps")
	tui.Infof("1. Distribute new certs to affected nodes")
	tui.Infof("2. Restart atlax-relay and/or atlax-agent services")
	tui.Infof("3. Verify connectivity: ats health")

	return nil
}

// ---------- certs inspect ----------

func newCertsInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect",
		Short: "Display details of a certificate file",
		RunE:  runCertsInspect,
	}
}

func runCertsInspect(cmd *cobra.Command, args []string) error {
	certPath := ""
	if len(args) > 0 {
		certPath = args[0]
	} else {
		certPath = tui.AskRequired("Certificate file path")
	}

	data, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", certPath, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("%s is not valid PEM", certPath)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("cannot parse certificate: %w", err)
	}

	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)

	tui.Header("Certificate Details")
	tui.Table([][]string{
		{"Subject", cert.Subject.String()},
		{"Issuer", cert.Issuer.String()},
		{"Serial", cert.SerialNumber.String()},
		{"Not Before", cert.NotBefore.Format("2006-01-02 15:04:05 UTC")},
		{"Not After", cert.NotAfter.Format("2006-01-02 15:04:05 UTC")},
		{"Days Left", fmt.Sprintf("%d", daysLeft)},
		{"Is CA", fmt.Sprintf("%v", cert.IsCA)},
		{"Key Usage", fmt.Sprintf("%v", cert.KeyUsage)},
	})

	if len(cert.DNSNames) > 0 {
		tui.Infof("DNS SANs: %v", cert.DNSNames)
	}
	if len(cert.IPAddresses) > 0 {
		tui.Infof("IP SANs: %v", cert.IPAddresses)
	}
	if len(cert.ExtKeyUsage) > 0 {
		usages := make([]string, len(cert.ExtKeyUsage))
		for i, u := range cert.ExtKeyUsage {
			switch u {
			case x509.ExtKeyUsageServerAuth:
				usages[i] = "ServerAuth"
			case x509.ExtKeyUsageClientAuth:
				usages[i] = "ClientAuth"
			default:
				usages[i] = fmt.Sprintf("%d", u)
			}
		}
		tui.Infof("Extended Key Usage: %v", usages)
	}

	return nil
}

// --- helpers ---

func backupCert(dir, filename string) (string, error) {
	src := filepath.Join(dir, filename)
	dst := filepath.Join(dir, filename+".bak."+time.Now().Format("20060102-150405"))
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	return dst, os.WriteFile(dst, data, 0644)
}

func splitAndTrim(s string) []string {
	parts := filepath.SplitList(s)
	if len(parts) <= 1 {
		// filepath.SplitList uses OS path separator; split by comma instead.
		rawParts := make([]string, 0)
		for _, p := range splitComma(s) {
			p = trimSpace(p)
			if p != "" {
				rawParts = append(rawParts, p)
			}
		}
		return rawParts
	}
	return parts
}

func splitComma(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == ',' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func trimSpace(s string) string {
	// Simple trim without importing strings for this helper file.
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
