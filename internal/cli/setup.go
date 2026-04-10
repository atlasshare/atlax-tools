package cli

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/certs"
	"github.com/atlasshare/atlax-tools/internal/config"
	"github.com/atlasshare/atlax-tools/internal/firewall"
	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/platform"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Provision a new relay or agent node",
	}
	cmd.AddCommand(newSetupRelayCmd(), newSetupAgentCmd())
	return cmd
}

// ---------- setup relay ----------

func newSetupRelayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "relay",
		Short: "Interactively provision a relay node on this machine",
		RunE:  runSetupRelay,
	}
}

func runSetupRelay(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Relay Setup")

	info := platInfo

	// Step 1: Binary source
	tui.Header("Step 1: Binary Installation")
	binaryPath := askBinarySource("atlax-relay", info)

	// Step 2: Config directory
	tui.Header("Step 2: Configuration")
	configDir := tui.Ask("Config directory", info.ConfigBasePath())
	certDir := tui.Ask("Certificate directory", filepath.Join(configDir, "certs"))

	// Step 3: Server settings
	listenAddr := tui.Ask("Agent listener address (mTLS)", "0.0.0.0:8443")
	adminAddr := tui.Ask("Admin/metrics address", "127.0.0.1:9090")

	// E6: warn if admin_addr port is already in use (catches Prometheus conflict)
	if _, port, err := net.SplitHostPort(adminAddr); err == nil {
		ln, listenErr := net.Listen("tcp", "127.0.0.1:"+port)
		if listenErr != nil {
			tui.Warnf("Port %s is already in use on localhost. If Prometheus is running on the same port, change one of them to avoid a bind conflict.", port)
		} else {
			ln.Close()
		}
	}

	maxAgents := tui.AskInt("Max concurrent agents", 100)
	maxStreams := tui.AskInt("Max streams per agent", 100)

	// Step 4: TLS domain
	tui.Header("Step 3: TLS Configuration")
	relayDomain := tui.Ask("Relay domain (for cert CN/SAN)", "relay.atlax.local")
	extraSANs := tui.Ask("Additional SANs (comma-separated IPs/domains)", "localhost,127.0.0.1")
	sans := strings.Split(extraSANs, ",")
	for i := range sans {
		sans[i] = strings.TrimSpace(sans[i])
	}

	generateCerts := tui.Confirm("Generate certificates now?", true)

	certBackend := certs.OpenSSL
	if generateCerts {
		if info.HasStepCLI && info.HasOpenSSL {
			idx := tui.Select("Certificate backend", []string{"OpenSSL", "step-ca (Smallstep)"}, 0)
			if idx == 1 {
				certBackend = certs.StepCLI
			}
		} else if info.HasStepCLI {
			certBackend = certs.StepCLI
		}
	}

	// Step 5: Initial customer
	tui.Header("Step 4: Initial Customer")
	addCustomer := tui.Confirm("Add an initial customer?", true)
	var initialCustomer *config.Customer
	if addCustomer {
		initialCustomer = askCustomerDetails()
	}

	// Step 6: Firewall
	tui.Header("Step 5: Firewall")

	// E4 part 2: detect cloud environment and warn about two-layer firewall
	if isCloudVM() {
		tui.Warnf("This appears to be a cloud VM. Remember that BOTH the cloud security group AND the host firewall (UFW/firewalld) must allow each port. Check both layers when debugging connectivity issues.")
	}

	configureFW := tui.Confirm("Configure firewall rules?", true)
	configureAWS := tui.Confirm("Print AWS Security Group commands?", false)
	var awsSGID, awsRegion string
	if configureAWS {
		awsSGID = tui.AskRequired("Security Group ID (sg-...)")
		awsRegion = tui.Ask("AWS Region", "us-east-1")
	}

	// Step 7: System service
	tui.Header("Step 6: Service Management")
	installService := tui.Confirm("Install as system service?", true)
	createGroup := tui.Confirm("Create 'atlax' system group? (controls file access)", true)
	createUser := false
	if installService {
		createUser = tui.Confirm("Also create dedicated 'atlax' service user? (runs the daemon)", true)
	}

	// --- Confirmation ---
	tui.Header("Summary")
	tui.Table([][]string{
		{"Binary", binaryPath},
		{"Config Dir", configDir},
		{"Cert Dir", certDir},
		{"Listen Addr", listenAddr},
		{"Admin Addr", adminAddr},
		{"Relay Domain", relayDomain},
		{"Certs", fmt.Sprintf("%v (backend: %s)", generateCerts, certBackend)},
		{"Firewall", fmt.Sprintf("%v", configureFW)},
		{"Service", fmt.Sprintf("%v", installService)},
	})
	if initialCustomer != nil {
		tui.Infof("Customer: %s with %d port(s)", initialCustomer.ID, len(initialCustomer.Ports))
	}
	fmt.Println()

	if !tui.Confirm("Proceed with relay setup?", true) {
		tui.Warn("Aborted.")
		return nil
	}

	// --- Execute with checklist ---
	cl := tui.NewChecklist("setup-relay", []tui.ChecklistStep{
		{ID: "dirs", Label: "Create directories", Status: tui.Pending},
		{ID: "binary", Label: "Install relay binary", Status: tui.Pending},
		{ID: "certs", Label: "Generate certificates", Status: tui.Pending},
		{ID: "config", Label: "Write relay configuration", Status: tui.Pending},
		{ID: "firewall", Label: "Configure firewall rules", Status: tui.Pending},
		{ID: "service", Label: "Install system service", Status: tui.Pending},
	})

	// Check for previous checkpoint.
	if prev, ok := cl.HasPrevious(); ok {
		tui.Warnf("Previous incomplete relay setup found (started %s)", prev.StartedAt)
		if tui.Confirm("Resume from checkpoint?", true) {
			cl.Resume(prev)
			tui.Successf("Resuming — completed steps will be skipped")
		}
	}
	cl.Render()

	// 1. Create directories
	if !cl.IsDone("dirs") {
		cl.MarkInProgress("dirs")
		if err := mkdirAll(dryRun, configDir, certDir); err != nil {
			cl.MarkFailed("dirs", err)
			cl.Summary()
			return err
		}
		cl.MarkDone("dirs")
	}

	// 2. Install binary
	if !cl.IsDone("binary") {
		cl.MarkInProgress("binary")
		installDir := info.BinaryInstallPath()
		if err := installBinary(dryRun, binaryPath, filepath.Join(installDir, "atlax-relay")); err != nil {
			cl.MarkFailed("binary", err)
			cl.Summary()
			return err
		}
		cl.MarkDone("binary")
	}

	// 3. Generate certs
	if !cl.IsDone("certs") && !cl.IsSkipped("certs") {
		if generateCerts {
			cl.MarkInProgress("certs")
			opts := certs.Opts{
				OutputDir:   certDir,
				Backend:     certBackend,
				RootCADays:  3650,
				InterDays:   1095,
				LeafDays:    90,
				RelayDomain: relayDomain,
				RelaySANs:   sans,
				DryRun:      dryRun,
			}
			if initialCustomer != nil {
				opts.CustomerID = initialCustomer.ID
			}
			if err := certs.GenerateFullPKI(opts); err != nil {
				cl.MarkFailed("certs", err)
				cl.Summary()
				return err
			}
			cl.MarkDone("certs")
		} else {
			tui.Infof("Using existing certs at %s", certDir)
			cl.MarkSkipped("certs")
		}
	}

	// 4. Write config
	configPath := filepath.Join(configDir, "relay.yaml")
	if !cl.IsDone("config") {
		cl.MarkInProgress("config")
		relayCfg := config.DefaultRelayConfig()
		relayCfg.Server.ListenAddr = listenAddr
		relayCfg.Server.AdminAddr = adminAddr
		relayCfg.Server.MaxAgents = maxAgents
		relayCfg.Server.MaxStreamsPerAgent = maxStreams
		relayCfg.TLS.CertFile = filepath.Join(certDir, "relay.crt")
		relayCfg.TLS.KeyFile = filepath.Join(certDir, "relay.key")
		relayCfg.TLS.CAFile = filepath.Join(certDir, "root-ca.crt")
		relayCfg.TLS.ClientCAFile = filepath.Join(certDir, "customer-ca.crt")

		if initialCustomer != nil {
			relayCfg.Customers = []config.Customer{*initialCustomer}
		}

		if dryRun {
			tui.DryRunf("Would write relay config to %s", configPath)
		} else {
			if err := config.WriteRelayConfig(configPath, relayCfg); err != nil {
				cl.MarkFailed("config", err)
				cl.Summary()
				return err
			}
			tui.Successf("Wrote %s", configPath)
		}
		logger.Log("write-config", configPath)
		cl.MarkDone("config")
	}

	// 5. Firewall
	if !cl.IsDone("firewall") && !cl.IsSkipped("firewall") {
		if configureFW {
			cl.MarkInProgress("firewall")
			var customerPorts []int
			if initialCustomer != nil {
				for _, p := range initialCustomer.Ports {
					customerPorts = append(customerPorts, p.Port)
				}
			}
			agentPort := 8443
			if parts := strings.SplitN(listenAddr, ":", 2); len(parts) == 2 {
				fmt.Sscanf(parts[1], "%d", &agentPort)
			}
			rules := firewall.GenerateRules(agentPort, customerPorts, 9090)
			if err := firewall.Apply(info, rules, dryRun); err != nil {
				tui.Warnf("Firewall configuration had issues: %s", err)
			}
			if configureAWS {
				firewall.PrintAWSSecurityGroup(rules, awsSGID, awsRegion)
			}
			cl.MarkDone("firewall")
		} else {
			tui.Infof("Skipped firewall configuration")
			cl.MarkSkipped("firewall")
		}
	}

	// 6. System service
	if !cl.IsDone("service") && !cl.IsSkipped("service") {
		if installService {
			cl.MarkInProgress("service")
			if err := installRelayService(info, configPath, createGroup, createUser, dryRun); err != nil {
				cl.MarkFailed("service", err)
				cl.Summary()
				return err
			}
			cl.MarkDone("service")
		} else {
			// Still create the group even without a service.
			if createGroup {
				if !dryRun {
					sysCmd := exec.Command("sudo", "groupadd", "--system", "atlax")
					if err := sysCmd.Run(); err != nil {
						tui.Warnf("Group creation failed (may already exist): %s", err)
					} else {
						tui.Successf("Created system group 'atlax'")
					}
				} else {
					tui.DryRunf("groupadd --system atlax")
				}
			}
			tui.Infof("Start manually: atlax-relay -config %s", configPath)
			cl.MarkSkipped("service")
		}
	}

	cl.Summary()
	tui.Infof("Log file: %s", logger.Path())

	return nil
}

// ---------- setup agent ----------

func newSetupAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Interactively provision an agent node on this machine",
		RunE:  runSetupAgent,
	}
}

func runSetupAgent(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Agent Setup")

	info := platInfo

	// Step 1: Binary
	tui.Header("Step 1: Binary Installation")
	binaryPath := askBinarySource("atlax-agent", info)

	// Step 2: Config
	tui.Header("Step 2: Configuration")
	configDir := tui.Ask("Config directory", info.ConfigBasePath())
	certDir := tui.Ask("Certificate directory", filepath.Join(configDir, "certs"))

	// Step 3: Relay connection
	tui.Header("Step 3: Relay Connection")
	relayAddr := tui.AskRequired("Relay address (host:port)")
	serverName := tui.Ask("Relay server name (for TLS verification)", "relay.atlax.local")
	keepaliveInterval := tui.Ask("Keepalive interval", "30s")
	keepaliveTimeout := tui.Ask("Keepalive timeout", "10s")

	// Step 4: Services
	tui.Header("Step 4: Local Services")
	var services []config.ServiceConfig
	for {
		tui.Infof("Registered services: %d", len(services))
		if len(services) > 0 && !tui.Confirm("Add another service?", false) {
			break
		}
		if len(services) == 0 && !tui.Confirm("Add a service?", true) {
			break
		}
		svc := askServiceDetails()
		services = append(services, svc)
		tui.Successf("Added service: %s → %s", svc.Name, svc.LocalAddr)
	}

	// Step 5: Cert source
	tui.Header("Step 5: Certificates")
	copyCerts := tui.Confirm("Copy certificates from a local path?", true)
	var certSource string
	if copyCerts {
		certSource = tui.AskPath("Path to certificate directory", "", true)
	}

	// Step 6: Service
	tui.Header("Step 6: Service Management")
	installService := tui.Confirm("Install as system service?", true)
	createGroup := tui.Confirm("Create 'atlax' system group? (controls file access)", true)
	createUser := false
	if installService {
		createUser = tui.Confirm("Also create dedicated 'atlax' service user? (runs the daemon)", true)
	}

	// --- Summary ---
	tui.Header("Summary")
	tui.Table([][]string{
		{"Binary", binaryPath},
		{"Config Dir", configDir},
		{"Relay", relayAddr},
		{"Server Name", serverName},
		{"Services", fmt.Sprintf("%d configured", len(services))},
		{"System Service", fmt.Sprintf("%v", installService)},
	})
	for _, s := range services {
		tui.Infof("  %s → %s (%s)", s.Name, s.LocalAddr, s.Protocol)
	}

	if !tui.Confirm("Proceed with agent setup?", true) {
		tui.Warn("Aborted.")
		return nil
	}

	// --- Execute with checklist ---
	cl := tui.NewChecklist("setup-agent", []tui.ChecklistStep{
		{ID: "dirs", Label: "Create directories", Status: tui.Pending},
		{ID: "binary", Label: "Install agent binary", Status: tui.Pending},
		{ID: "certs", Label: "Copy certificates", Status: tui.Pending},
		{ID: "config", Label: "Write agent configuration", Status: tui.Pending},
		{ID: "service", Label: "Install system service", Status: tui.Pending},
	})

	// Check for previous checkpoint.
	if prev, ok := cl.HasPrevious(); ok {
		tui.Warnf("Previous incomplete agent setup found (started %s)", prev.StartedAt)
		if tui.Confirm("Resume from checkpoint?", true) {
			cl.Resume(prev)
			tui.Successf("Resuming — completed steps will be skipped")
		}
	}
	cl.Render()

	// 1. Directories
	if !cl.IsDone("dirs") {
		cl.MarkInProgress("dirs")
		if err := mkdirAll(dryRun, configDir, certDir); err != nil {
			cl.MarkFailed("dirs", err)
			cl.Summary()
			return err
		}
		cl.MarkDone("dirs")
	}

	// 2. Binary
	if !cl.IsDone("binary") {
		cl.MarkInProgress("binary")
		installDir := info.BinaryInstallPath()
		if err := installBinary(dryRun, binaryPath, filepath.Join(installDir, "atlax-agent")); err != nil {
			cl.MarkFailed("binary", err)
			cl.Summary()
			return err
		}
		cl.MarkDone("binary")
	}

	// 3. Certs
	if !cl.IsDone("certs") && !cl.IsSkipped("certs") {
		if copyCerts && certSource != "" {
			cl.MarkInProgress("certs")
			certFiles := []string{"agent.crt", "agent.key", "relay-ca.crt", "root-ca.crt"}
			var certErr error
			for _, f := range certFiles {
				src := filepath.Join(certSource, f)
				dst := filepath.Join(certDir, f)
				if dryRun {
					tui.DryRunf("cp %s %s", src, dst)
				} else {
					data, err := os.ReadFile(src)
					if err != nil {
						tui.Warnf("Cannot read %s: %s", src, err)
						continue
					}
					perm := os.FileMode(0644)
					if strings.HasSuffix(f, ".key") {
						perm = 0600
					}
					if err := os.WriteFile(dst, data, perm); err != nil {
						certErr = fmt.Errorf("cannot write %s: %w", dst, err)
						break
					}
					tui.Successf("Copied %s", f)
				}
			}
			if certErr != nil {
				cl.MarkFailed("certs", certErr)
				cl.Summary()
				return certErr
			}
			cl.MarkDone("certs")
		} else {
			tui.Infof("Provide certs manually at %s", certDir)
			cl.MarkSkipped("certs")
		}
	}

	// 4. Config
	configPath := filepath.Join(configDir, "agent.yaml")
	if !cl.IsDone("config") {
		cl.MarkInProgress("config")
		agentCfg := config.DefaultAgentConfig()
		agentCfg.Relay.Addr = relayAddr
		agentCfg.Relay.ServerName = serverName
		agentCfg.Relay.KeepaliveInterval = keepaliveInterval
		agentCfg.Relay.KeepaliveTimeout = keepaliveTimeout
		agentCfg.TLS.CertFile = filepath.Join(certDir, "agent.crt")
		agentCfg.TLS.KeyFile = filepath.Join(certDir, "agent.key")
		agentCfg.TLS.CAFile = filepath.Join(certDir, "relay-ca.crt")
		agentCfg.Services = services

		if dryRun {
			tui.DryRunf("Would write agent config to %s", configPath)
		} else {
			if err := config.WriteAgentConfig(configPath, agentCfg); err != nil {
				cl.MarkFailed("config", err)
				cl.Summary()
				return err
			}
			tui.Successf("Wrote %s", configPath)
		}
		cl.MarkDone("config")
	}

	// 5. Service
	if !cl.IsDone("service") && !cl.IsSkipped("service") {
		if installService {
			cl.MarkInProgress("service")
			if err := installAgentService(info, configPath, createGroup, createUser, dryRun); err != nil {
				cl.MarkFailed("service", err)
				cl.Summary()
				return err
			}
			cl.MarkDone("service")
		} else {
			tui.Infof("Start manually: atlax-agent -config %s", configPath)
			cl.MarkSkipped("service")
		}
	}

	cl.Summary()
	tui.Infof("Log file: %s", logger.Path())

	return nil
}

// ---------- shared helpers ----------

func askBinarySource(name string, info platform.Info) string {
	idx := tui.Select("Binary source", []string{
		"Download from GitHub releases",
		"Build from source (requires Go)",
		"Specify local path",
	}, 0)

	switch idx {
	case 0:
		// Download: prompt for version, build URL
		ver := tui.Ask("Version", "latest")
		url := fmt.Sprintf("https://github.com/atlasshare/atlax/releases/download/%s/%s-%s-%s",
			ver, name, info.GoOS, info.GoArch)
		tui.Infof("Download URL: %s", url)

		dest := filepath.Join(os.TempDir(), name)
		if dryRun {
			tui.DryRunf("Would download %s to %s", url, dest)
			return dest
		}
		tui.Infof("Downloading %s...", name)
		if err := downloadFile(url, dest); err != nil {
			tui.Warnf("Download failed: %s", err)
			tui.Infof("Falling back to local path")
			return tui.AskRequired("Path to " + name + " binary")
		}
		_ = os.Chmod(dest, 0755)
		tui.Successf("Downloaded to %s", dest)
		return dest

	case 1:
		// Build from source
		srcDir := tui.Ask("Atlax source directory", filepath.Join(os.Getenv("HOME"), "projects/atlax"))
		cmdStr := "relay"
		if strings.Contains(name, "agent") {
			cmdStr = "agent"
		}
		dest := filepath.Join(os.TempDir(), name)
		if dryRun {
			tui.DryRunf("Would build: go build -o %s ./cmd/%s/", dest, cmdStr)
			return dest
		}
		tui.Infof("Building %s from %s...", name, srcDir)
		buildCmd := exec.Command("go", "build", "-o", dest, fmt.Sprintf("./cmd/%s/", cmdStr))
		buildCmd.Dir = srcDir
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			tui.Failf("Build failed: %s", err)
			return tui.AskRequired("Path to " + name + " binary")
		}
		tui.Successf("Built %s", dest)
		return dest

	default:
		return tui.AskRequired("Path to " + name + " binary")
	}
}

func askCustomerDetails() *config.Customer {
	prefix := tui.Ask("Customer ID prefix", "customer")
	idSuffix := tui.Ask("Customer ID suffix (e.g., dev-001 or UUID)", "dev-001")
	customerID := fmt.Sprintf("%s-%s", prefix, idSuffix)

	cust := &config.Customer{
		ID:             customerID,
		MaxConnections: tui.AskInt("Max agent connections", 1),
		MaxStreams:      tui.AskInt("Max concurrent streams", 100),
	}

	// Add ports
	for {
		if len(cust.Ports) > 0 && !tui.Confirm("Add another port?", false) {
			break
		}
		port := tui.AskIntRange("Port number", 18080, 18000, 18999)
		service := tui.SelectString("Service type", config.SupportedServices(), 0)
		listenAddr := tui.Ask("Listen address", "127.0.0.1")
		desc := tui.Ask("Description", "")

		cust.Ports = append(cust.Ports, config.PortConfig{
			Port:        port,
			Service:     service,
			Description: desc,
			ListenAddr:  listenAddr,
		})
		tui.Successf("Added port %d → %s", port, service)
	}

	return cust
}

func askServiceDetails() config.ServiceConfig {
	name := tui.SelectString("Service name", config.SupportedServices(), 0)
	if name == "custom" {
		name = tui.AskRequired("Custom service name")
	}
	localAddr := tui.AskRequired("Local address (host:port)")
	protocol := tui.Ask("Protocol", "tcp")
	desc := tui.Ask("Description", "")

	return config.ServiceConfig{
		Name:        name,
		LocalAddr:   localAddr,
		Protocol:    protocol,
		Description: desc,
	}
}

func mkdirAll(dry bool, dirs ...string) error {
	for _, d := range dirs {
		if dry {
			tui.DryRunf("mkdir -p %s", d)
			continue
		}
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("cannot create %s: %w", d, err)
		}
		tui.Successf("Created %s", d)
		logger.Log("mkdir", d)
	}
	return nil
}

func installBinary(dry bool, src, dst string) error {
	if dry {
		tui.DryRunf("cp %s %s", src, dst)
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("cannot read binary: %w", err)
	}
	if err := os.WriteFile(dst, data, 0755); err != nil {
		// Try with sudo
		tui.Infof("Direct write failed, trying with sudo...")
		cmd := exec.Command("sudo", "cp", src, dst)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cannot install binary: %w", err)
		}
		_ = exec.Command("sudo", "chmod", "755", dst).Run()
	}
	tui.Successf("Installed %s", dst)
	logger.Log("install-binary", dst)
	return nil
}

func installRelayService(info platform.Info, configPath string, createGroup, createUser, dry bool) error {
	if info.InitSystem != platform.Systemd {
		tui.Warnf("Automatic service installation only supported for systemd (detected: %s)", info.InitSystem)
		tui.Infof("Please create a service manually for %s", info.InitSystem)
		return nil
	}

	// Group first — controls file access to configs, certs, logs.
	if createGroup {
		if dry {
			tui.DryRunf("groupadd --system atlax")
		} else {
			cmd := exec.Command("sudo", "groupadd", "--system", "atlax")
			if err := cmd.Run(); err != nil {
				tui.Warnf("Group creation failed (may already exist): %s", err)
			} else {
				tui.Successf("Created system group 'atlax'")
			}
			logger.Log("create-group", "atlax")
		}
	}

	// Optional service user — runs the daemon under systemd.
	if createUser {
		if dry {
			tui.DryRunf("useradd --system --no-create-home --shell /usr/sbin/nologin -g atlax atlax")
		} else {
			cmd := exec.Command("sudo", "useradd", "--system", "--no-create-home",
				"--shell", "/usr/sbin/nologin", "-g", "atlax", "atlax")
			if err := cmd.Run(); err != nil {
				tui.Warnf("User creation failed (may already exist): %s", err)
			} else {
				tui.Successf("Created system user 'atlax' in group 'atlax'")
			}
			logger.Log("create-user", "atlax")
		}
	}

	// Set group ownership on config directory.
	configDir := filepath.Dir(configPath)
	if !dry {
		_ = exec.Command("sudo", "chgrp", "-R", "atlax", configDir).Run()
		_ = exec.Command("sudo", "chmod", "-R", "g+r", configDir).Run()
		// Keys are group-readable but not world-readable.
		_ = exec.Command("sudo", "find", configDir, "-name", "*.key", "-exec",
			"chmod", "640", "{}", ";").Run()
		tui.Successf("Set group 'atlax' ownership on %s", configDir)
	} else {
		tui.DryRunf("chgrp -R atlax %s && chmod -R g+r %s", configDir, configDir)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Atlax Relay — mTLS reverse tunnel relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlax
Group=atlax
ExecStart=/usr/local/bin/atlax-relay -config %s
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
MemoryDenyWriteExecute=yes
LockPersonality=yes
ReadWritePaths=/var/log/atlax
ReadOnlyPaths=%s

# Allow binding to privileged ports
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`, configPath, filepath.Dir(configPath))

	servicePath := "/etc/systemd/system/atlax-relay.service"

	if dry {
		tui.DryRunf("Would write %s", servicePath)
		tui.DryRunf("systemctl daemon-reload && systemctl enable --now atlax-relay")
		return nil
	}

	tmpFile := filepath.Join(os.TempDir(), "atlax-relay.service")
	if err := os.WriteFile(tmpFile, []byte(unit), 0644); err != nil {
		return err
	}

	cmds := [][]string{
		{"sudo", "cp", tmpFile, servicePath},
		{"sudo", "systemctl", "daemon-reload"},
		{"sudo", "systemctl", "enable", "atlax-relay"},
		{"sudo", "systemctl", "start", "atlax-relay"},
	}

	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("command %s failed: %w", strings.Join(c, " "), err)
		}
	}

	tui.Successf("Installed and started atlax-relay.service")
	logger.Log("install-service", "atlax-relay")
	return nil
}

func installAgentService(info platform.Info, configPath string, createGroup, createUser, dry bool) error {
	if info.InitSystem != platform.Systemd {
		tui.Warnf("Automatic service installation only supported for systemd (detected: %s)", info.InitSystem)
		return nil
	}

	if createGroup {
		if dry {
			tui.DryRunf("groupadd --system atlax")
		} else {
			cmd := exec.Command("sudo", "groupadd", "--system", "atlax")
			if err := cmd.Run(); err != nil {
				tui.Warnf("Group creation failed (may already exist): %s", err)
			} else {
				tui.Successf("Created system group 'atlax'")
			}
		}
	}

	if createUser {
		if dry {
			tui.DryRunf("useradd --system --no-create-home --shell /usr/sbin/nologin -g atlax atlax")
		} else {
			cmd := exec.Command("sudo", "useradd", "--system", "--no-create-home",
				"--shell", "/usr/sbin/nologin", "-g", "atlax", "atlax")
			_ = cmd.Run()
		}
	}

	// Set group ownership.
	configDir := filepath.Dir(configPath)
	if !dry {
		_ = exec.Command("sudo", "chgrp", "-R", "atlax", configDir).Run()
		_ = exec.Command("sudo", "chmod", "-R", "g+r", configDir).Run()
		_ = exec.Command("sudo", "find", configDir, "-name", "*.key", "-exec",
			"chmod", "640", "{}", ";").Run()
	}

	unit := fmt.Sprintf(`[Unit]
Description=Atlax Agent — mTLS reverse tunnel agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlax
Group=atlax
ExecStart=/usr/local/bin/atlax-agent -config %s
Restart=on-failure
RestartSec=5s
WatchdogSec=30s
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
MemoryDenyWriteExecute=yes
LockPersonality=yes
ReadOnlyPaths=%s

[Install]
WantedBy=multi-user.target
`, configPath, filepath.Dir(configPath))

	servicePath := "/etc/systemd/system/atlax-agent.service"

	if dry {
		tui.DryRunf("Would write %s", servicePath)
		tui.DryRunf("systemctl daemon-reload && systemctl enable --now atlax-agent")
		return nil
	}

	tmpFile := filepath.Join(os.TempDir(), "atlax-agent.service")
	if err := os.WriteFile(tmpFile, []byte(unit), 0644); err != nil {
		return err
	}

	cmds := [][]string{
		{"sudo", "cp", tmpFile, servicePath},
		{"sudo", "systemctl", "daemon-reload"},
		{"sudo", "systemctl", "enable", "atlax-agent"},
		{"sudo", "systemctl", "start", "atlax-agent"},
	}

	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("command %s failed: %w", strings.Join(c, " "), err)
		}
	}

	tui.Successf("Installed and started atlax-agent.service")
	logger.Log("install-service", "atlax-agent")
	return nil
}

func downloadFile(url, dest string) error {
	// Try curl first, then wget.
	if _, err := exec.LookPath("curl"); err == nil {
		cmd := exec.Command("curl", "-fsSL", "-o", dest, url)
		return cmd.Run()
	}
	if _, err := exec.LookPath("wget"); err == nil {
		cmd := exec.Command("wget", "-q", "-O", dest, url)
		return cmd.Run()
	}
	return fmt.Errorf("neither curl nor wget found — install one and retry")
}

// isCloudVM tries to reach the AWS/GCP/Azure instance metadata endpoint.
// Returns true if any responds within 500ms, indicating a cloud VM.
func isCloudVM() bool {
	endpoints := []string{
		"http://169.254.169.254/latest/meta-data/",  // AWS
		"http://metadata.google.internal/",           // GCP
		"http://169.254.169.254/metadata/instance",   // Azure
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for _, ep := range endpoints {
		resp, err := client.Get(ep) //nolint:noctx // fire-and-forget probe
		if err == nil {
			resp.Body.Close()
			return true
		}
	}
	return false
}
