package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/certs"
	"github.com/atlasshare/atlax-tools/internal/config"
	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newCustomerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "customer",
		Short: "Manage customers on a relay",
	}
	cmd.AddCommand(newCustomerAddCmd(), newCustomerListCmd())
	return cmd
}

func newCustomerAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a new customer to the relay configuration",
		RunE:  runCustomerAdd,
	}
}

func runCustomerAdd(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Add Customer")

	// Load existing relay config
	configPath := tui.Ask("Relay config path", "/etc/atlax/relay.yaml")
	relayCfg, err := config.ReadRelayConfig(configPath)
	if err != nil {
		return fmt.Errorf("cannot read relay config: %w", err)
	}

	tui.Infof("Loaded relay config with %d existing customer(s)", len(relayCfg.Customers))
	for _, c := range relayCfg.Customers {
		tui.Infof("  %s (%d ports)", c.ID, len(c.Ports))
	}

	// Config strategy
	strategy := tui.Select("Config update strategy", []string{
		"In-place edit (preserve manual tweaks)",
		"Regenerate full config (clean state)",
	}, 0)

	// Customer details
	cust := askCustomerDetails()

	// Check for conflicts
	if existing := config.CustomerByID(relayCfg, cust.ID); existing != nil {
		return fmt.Errorf("customer %q already exists", cust.ID)
	}

	// Port conflict check
	for _, p := range cust.Ports {
		for _, c := range relayCfg.Customers {
			for _, ep := range c.Ports {
				if ep.Port == p.Port {
					return fmt.Errorf("port %d already allocated to customer %q", p.Port, c.ID)
				}
			}
		}
	}

	// Generate certs?
	generateCerts := tui.Confirm("Generate agent certificate for this customer?", true)
	var certDir string
	if generateCerts {
		certDir = tui.Ask("Certificate directory", "/etc/atlax/certs")
		backend := certs.DetectBackend()
		opts := certs.Opts{
			OutputDir:  certDir,
			Backend:    backend,
			LeafDays:   90,
			CustomerID: cust.ID,
			DryRun:     dryRun,
		}
		tui.Infof("Issuing agent cert for %s...", cust.ID)
		if err := certs.IssueAgentCert(opts); err != nil {
			tui.Warnf("Cert generation failed: %s", err)
			tui.Infof("You can generate certs later with: ats certs issue")
		} else {
			tui.Successf("Agent cert issued for %s", cust.ID)
		}
	}

	// Summary
	tui.Header("Summary")
	tui.Table([][]string{
		{"Customer ID", cust.ID},
		{"Max Connections", fmt.Sprintf("%d", cust.MaxConnections)},
		{"Max Streams", fmt.Sprintf("%d", cust.MaxStreams)},
		{"Ports", fmt.Sprintf("%d", len(cust.Ports))},
		{"Strategy", []string{"in-place", "regenerate"}[strategy]},
	})
	for _, p := range cust.Ports {
		tui.Infof("  :%d → %s (listen: %s)", p.Port, p.Service, p.ListenAddr)
	}

	if !tui.Confirm("Apply changes?", true) {
		tui.Warn("Aborted.")
		return nil
	}

	// Backup
	if !dryRun {
		if bak, err := config.BackupFile(configPath); err == nil {
			tui.Successf("Backed up config to %s", bak)
		}
	}

	// Apply
	if strategy == 0 {
		// In-place
		if err := config.AddCustomer(relayCfg, *cust); err != nil {
			return err
		}
	} else {
		// Regenerate
		relayCfg.Customers = append(relayCfg.Customers, *cust)
	}

	if dryRun {
		tui.DryRunf("Would write updated config to %s", configPath)
	} else {
		if err := config.WriteRelayConfig(configPath, relayCfg); err != nil {
			return err
		}
		tui.Successf("Updated %s", configPath)
	}

	logger.Log("add-customer", cust.ID)

	tui.Header("Next Steps")
	tui.Infof("1. Restart the relay: sudo systemctl restart atlax-relay")
	if generateCerts {
		tui.Infof("2. Distribute agent certs from %s to the customer node", certDir)
	}
	tui.Infof("3. Configure the agent with matching service names")

	return nil
}

func newCustomerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all customers and their port allocations",
		RunE:  runCustomerList,
	}
}

func runCustomerList(cmd *cobra.Command, args []string) error {
	configPath := tui.Ask("Relay config path", "/etc/atlax/relay.yaml")
	relayCfg, err := config.ReadRelayConfig(configPath)
	if err != nil {
		return fmt.Errorf("cannot read relay config: %w", err)
	}

	tui.Header("Customer Port Allocations")

	if len(relayCfg.Customers) == 0 {
		tui.Infof("No customers configured")
		return nil
	}

	for _, c := range relayCfg.Customers {
		tui.Infof("%s (max_conn: %d, max_streams: %d)", c.ID, c.MaxConnections, c.MaxStreams)
		for _, p := range c.Ports {
			listen := p.ListenAddr
			if listen == "" {
				listen = "0.0.0.0"
			}
			desc := p.Description
			if desc == "" {
				desc = p.Service
			}
			fmt.Printf("    :%d  →  %-12s  listen: %-12s  %s\n", p.Port, p.Service, listen, desc)
		}
		tui.Divider()
	}

	return nil
}
