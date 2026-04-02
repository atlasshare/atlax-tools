// Package firewall generates and optionally executes firewall rules
// for the detected platform, plus AWS security group CLI commands.
package firewall

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/platform"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

// Rule describes a firewall port rule.
type Rule struct {
	Port        int
	Protocol    string // tcp, udp
	Source      string // CIDR or "any"
	Description string
}

// GenerateRules returns the standard set of rules for a relay deployment.
func GenerateRules(agentPort int, customerPorts []int, adminPort int) []Rule {
	rules := []Rule{
		{Port: 22, Protocol: "tcp", Source: "any", Description: "SSH"},
		{Port: 80, Protocol: "tcp", Source: "any", Description: "HTTP (ACME challenges)"},
		{Port: 443, Protocol: "tcp", Source: "any", Description: "HTTPS (Caddy)"},
		{Port: agentPort, Protocol: "tcp", Source: "any", Description: "Atlax agent mTLS"},
	}

	for _, p := range customerPorts {
		rules = append(rules, Rule{
			Port:        p,
			Protocol:    "tcp",
			Source:      "127.0.0.1",
			Description: fmt.Sprintf("Customer service port %d (localhost only)", p),
		})
	}

	if adminPort > 0 {
		rules = append(rules, Rule{
			Port:        adminPort,
			Protocol:    "tcp",
			Source:      "127.0.0.1",
			Description: "Atlax admin/metrics (localhost only)",
		})
	}

	return rules
}

// ApplyUFW configures UFW with the given rules.
func ApplyUFW(rules []Rule, dryRun bool) error {
	cmds := []string{
		"sudo ufw default deny incoming",
		"sudo ufw default allow outgoing",
	}

	for _, r := range rules {
		if r.Source == "127.0.0.1" || r.Source == "localhost" {
			// Localhost-only rules use listen_addr binding, not UFW.
			// But we can add an explicit deny for external access.
			cmds = append(cmds, fmt.Sprintf("# Port %d: bound to 127.0.0.1 (no UFW rule needed)", r.Port))
			continue
		}
		if r.Source != "" && r.Source != "any" {
			cmds = append(cmds, fmt.Sprintf("sudo ufw allow from %s to any port %d proto %s comment '%s'",
				r.Source, r.Port, r.Protocol, r.Description))
		} else {
			cmds = append(cmds, fmt.Sprintf("sudo ufw allow %d/%s comment '%s'",
				r.Port, r.Protocol, r.Description))
		}
	}
	cmds = append(cmds, "sudo ufw --force enable")

	return executeOrPrint(cmds, dryRun, "UFW")
}

// ApplyFirewalld configures firewalld with the given rules.
func ApplyFirewalld(rules []Rule, dryRun bool) error {
	var cmds []string
	for _, r := range rules {
		if r.Source == "127.0.0.1" || r.Source == "localhost" {
			cmds = append(cmds, fmt.Sprintf("# Port %d: bound to 127.0.0.1 (no firewalld rule needed)", r.Port))
			continue
		}
		cmds = append(cmds, fmt.Sprintf("sudo firewall-cmd --permanent --add-port=%d/%s",
			r.Port, r.Protocol))
	}
	cmds = append(cmds, "sudo firewall-cmd --reload")
	return executeOrPrint(cmds, dryRun, "firewalld")
}

// ApplyIptables generates iptables rules.
func ApplyIptables(rules []Rule, dryRun bool) error {
	cmds := []string{
		"sudo iptables -P INPUT DROP",
		"sudo iptables -A INPUT -i lo -j ACCEPT",
		"sudo iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT",
	}
	for _, r := range rules {
		if r.Source == "127.0.0.1" || r.Source == "localhost" {
			continue
		}
		cmds = append(cmds, fmt.Sprintf("sudo iptables -A INPUT -p %s --dport %d -j ACCEPT",
			r.Protocol, r.Port))
	}
	return executeOrPrint(cmds, dryRun, "iptables")
}

// PrintAWSSecurityGroup prints the AWS CLI commands for security group rules.
func PrintAWSSecurityGroup(rules []Rule, sgID, region string) {
	tui.Header("AWS Security Group Commands")
	tui.Infof("Security Group: %s  Region: %s", sgID, region)
	fmt.Println()

	for _, r := range rules {
		if r.Source == "127.0.0.1" || r.Source == "localhost" {
			continue // Localhost ports don't need SG rules.
		}
		cidr := "0.0.0.0/0"
		if r.Source != "" && r.Source != "any" {
			cidr = r.Source
			if !strings.Contains(cidr, "/") {
				cidr += "/32"
			}
		}
		fmt.Printf("  aws ec2 authorize-security-group-ingress \\\n")
		fmt.Printf("    --group-id %s \\\n", sgID)
		fmt.Printf("    --protocol %s \\\n", r.Protocol)
		fmt.Printf("    --port %d \\\n", r.Port)
		fmt.Printf("    --cidr %s \\\n", cidr)
		fmt.Printf("    --region %s \\\n", region)
		fmt.Printf("    --tag-specifications 'ResourceType=security-group-rule,Tags=[{Key=Name,Value=%s}]'\n\n",
			r.Description)
	}
}

// Apply uses the detected platform to choose the right firewall tool.
func Apply(info platform.Info, rules []Rule, dryRun bool) error {
	switch info.Firewall {
	case platform.UFW:
		return ApplyUFW(rules, dryRun)
	case platform.Firewalld:
		return ApplyFirewalld(rules, dryRun)
	case platform.Iptables, platform.Nftables:
		return ApplyIptables(rules, dryRun)
	default:
		tui.Warn("No supported firewall detected. Please configure manually:")
		for _, r := range rules {
			fmt.Printf("  Allow port %d/%s from %s (%s)\n", r.Port, r.Protocol, r.Source, r.Description)
		}
		return nil
	}
}

func executeOrPrint(cmds []string, dryRun bool, tool string) error {
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "#") {
			tui.Infof("%s", cmd)
			continue
		}
		if dryRun {
			tui.DryRunf("%s", cmd)
			logger.Log("firewall", fmt.Sprintf("[dry-run] %s", cmd))
			continue
		}
		tui.Infof("Running: %s", cmd)
		logger.Log("firewall", cmd)
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			continue
		}
		c := exec.Command(parts[0], parts[1:]...)
		out, err := c.CombinedOutput()
		if err != nil {
			tui.Failf("%s: %s", cmd, strings.TrimSpace(string(out)))
			return fmt.Errorf("%s failed: %w", cmd, err)
		}
		tui.Successf("%s", cmd)
	}
	return nil
}
