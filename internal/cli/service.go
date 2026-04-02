package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/caddy"
	"github.com/atlasshare/atlax-tools/internal/config"
	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services — add routes to relay + agent configs",
	}
	cmd.AddCommand(newServiceAddCmd())
	return cmd
}

func newServiceAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a new service to relay and agent configs",
		RunE:  runServiceAdd,
	}
}

func runServiceAdd(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Add Service")

	// --- Relay side ---
	tui.Header("Relay Configuration")
	relayConfigPath := tui.Ask("Relay config path", "/etc/atlax/relay.yaml")
	relayCfg, err := config.ReadRelayConfig(relayConfigPath)
	if err != nil {
		return fmt.Errorf("cannot read relay config: %w", err)
	}

	// Pick customer
	customerIDs := config.ListCustomerIDs(relayCfg)
	if len(customerIDs) == 0 {
		return fmt.Errorf("no customers in relay config — add one first with: ats customer add")
	}

	tui.Infof("Available customers:")
	customerID := tui.SelectString("Customer", customerIDs, 0)

	// Show existing ports for this customer
	cust := config.CustomerByID(relayCfg, customerID)
	if cust != nil && len(cust.Ports) > 0 {
		tui.Infof("Existing ports for %s:", customerID)
		for _, p := range cust.Ports {
			fmt.Printf("    :%d → %s\n", p.Port, p.Service)
		}
	}

	// Service details
	serviceName := tui.SelectString("Service type", config.SupportedServices(), 0)
	if serviceName == "custom" {
		serviceName = tui.AskRequired("Custom service name")
	}

	// Auto-allocate port
	suggestedPort, err := config.FindNextPort(relayCfg, 18000, 18999)
	if err != nil {
		tui.Warnf("Port auto-allocation failed: %s", err)
		suggestedPort = 18080
	}
	port := tui.AskIntRange("Relay port", suggestedPort, 18000, 18999)
	listenAddr := tui.Ask("Relay listen address", "127.0.0.1")
	description := tui.Ask("Description", "")

	portCfg := config.PortConfig{
		Port:        port,
		Service:     serviceName,
		Description: description,
		ListenAddr:  listenAddr,
	}

	// --- Agent side ---
	tui.Header("Agent Configuration")
	updateAgent := tui.Confirm("Also update agent config?", true)
	var agentConfigPath string
	var agentCfg *config.AgentConfig
	var localAddr string

	if updateAgent {
		agentConfigPath = tui.Ask("Agent config path", "/etc/atlax/agent.yaml")
		agentCfg, err = config.ReadAgentConfig(agentConfigPath)
		if err != nil {
			return fmt.Errorf("cannot read agent config: %w", err)
		}
		localAddr = tui.AskRequired("Local service address (e.g., 127.0.0.1:3000)")
	}

	// --- Caddy ---
	updateCaddy := false
	var caddyDomain string
	isHTTPService := serviceName == "http" || serviceName == "https" || serviceName == "api" || serviceName == "websocket"

	if isHTTPService {
		updateCaddy = tui.Confirm("Add a Caddy reverse proxy block for this service?", true)
		if updateCaddy {
			caddyDomain = tui.AskRequired("Domain for this service (e.g., app.rubendev.io)")
		}
	}

	// --- Config strategy ---
	strategy := tui.Select("Config update strategy", []string{
		"In-place edit (preserve manual tweaks)",
		"Regenerate full config (clean state)",
	}, 0)
	_ = strategy // Both paths use AddPort/AddService which handle dedup.

	// --- Summary ---
	tui.Header("Summary")
	tui.Table([][]string{
		{"Customer", customerID},
		{"Service", serviceName},
		{"Relay Port", fmt.Sprintf("%d (listen: %s)", port, listenAddr)},
	})
	if updateAgent {
		tui.Infof("Agent: %s → %s", serviceName, localAddr)
	}
	if updateCaddy {
		tui.Infof("Caddy: %s → localhost:%d", caddyDomain, port)
	}

	if !tui.Confirm("Apply changes?", true) {
		tui.Warn("Aborted.")
		return nil
	}

	// --- Execute ---
	totalSteps := 3
	step := 0

	// 1. Relay config
	step++
	tui.Step(step, totalSteps, "Updating relay config")
	if !dryRun {
		if bak, err := config.BackupFile(relayConfigPath); err == nil {
			tui.Successf("Backed up to %s", bak)
		}
	}
	if err := config.AddPort(relayCfg, customerID, portCfg); err != nil {
		return err
	}
	if dryRun {
		tui.DryRunf("Would write %s", relayConfigPath)
	} else {
		if err := config.WriteRelayConfig(relayConfigPath, relayCfg); err != nil {
			return err
		}
		tui.Successf("Updated %s", relayConfigPath)
	}
	logger.Log("add-port", fmt.Sprintf("%s:%d→%s", customerID, port, serviceName))

	// 2. Agent config
	step++
	tui.Step(step, totalSteps, "Updating agent config")
	if updateAgent && agentCfg != nil {
		if !dryRun {
			if bak, err := config.BackupFile(agentConfigPath); err == nil {
				tui.Successf("Backed up to %s", bak)
			}
		}
		svc := config.ServiceConfig{
			Name:      serviceName,
			LocalAddr: localAddr,
			Protocol:  "tcp",
		}
		if err := config.AddService(agentCfg, svc); err != nil {
			return err
		}
		if dryRun {
			tui.DryRunf("Would write %s", agentConfigPath)
		} else {
			if err := config.WriteAgentConfig(agentConfigPath, agentCfg); err != nil {
				return err
			}
			tui.Successf("Updated %s", agentConfigPath)
		}
		logger.Log("add-service", fmt.Sprintf("%s→%s", serviceName, localAddr))
	} else {
		tui.Infof("Skipped agent config update")
	}

	// 3. Caddy
	step++
	tui.Step(step, totalSteps, "Caddy configuration")
	if updateCaddy {
		caddyfilePath := tui.Ask("Caddyfile path", caddy.DefaultCaddyfilePath())
		block := caddy.NewServiceBlock(caddyDomain, port)
		if err := caddy.AppendToFile(caddyfilePath, block, dryRun); err != nil {
			return err
		}
		logger.Log("add-caddy-block", caddyDomain)
	} else {
		tui.Infof("Skipped Caddy configuration")
	}

	// --- Next steps ---
	tui.Header("Next Steps")
	restartCmds := []string{}
	restartCmds = append(restartCmds, "sudo systemctl restart atlax-relay")
	if updateAgent {
		restartCmds = append(restartCmds, "sudo systemctl restart atlax-agent")
	}
	if updateCaddy {
		restartCmds = append(restartCmds, "sudo systemctl reload caddy")
	}
	tui.Infof("Restart services: %s", strings.Join(restartCmds, " && "))

	return nil
}
