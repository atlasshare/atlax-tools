package cli

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/config"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Run diagnostics on an atlax deployment",
		RunE:  runHealth,
	}
}

func runHealth(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Atlax Health Check")

	nodeType := tui.Select("Node type", []string{"Relay", "Agent", "Both (full stack)"}, 0)

	passed := 0
	failed := 0
	warned := 0

	check := func(name string, fn func() error) {
		err := fn()
		if err == nil {
			tui.Successf("%s", name)
			passed++
		} else {
			errStr := err.Error()
			if strings.HasPrefix(errStr, "WARN:") {
				tui.Warnf("%s: %s", name, strings.TrimPrefix(errStr, "WARN:"))
				warned++
			} else {
				tui.Failf("%s: %s", name, err)
				failed++
			}
		}
	}

	// --- Relay checks ---
	if nodeType == 0 || nodeType == 2 {
		tui.Header("Relay Health")

		configPath := tui.Ask("Relay config path", "/etc/atlax/relay.yaml")
		relayCfg, cfgErr := config.ReadRelayConfig(configPath)

		check("Config file readable", func() error {
			if cfgErr != nil {
				return cfgErr
			}
			return nil
		})

		if relayCfg != nil {
			check("Config valid (has customers)", func() error {
				if len(relayCfg.Customers) == 0 {
					return fmt.Errorf("no customers configured")
				}
				return nil
			})

			check("TLS cert files exist", func() error {
				files := []string{relayCfg.TLS.CertFile, relayCfg.TLS.KeyFile, relayCfg.TLS.CAFile, relayCfg.TLS.ClientCAFile}
				for _, f := range files {
					if _, err := os.Stat(f); err != nil {
						return fmt.Errorf("%s missing", f)
					}
				}
				return nil
			})

			check("Relay cert not expired", func() error {
				return checkCertExpiry(relayCfg.TLS.CertFile, 30)
			})

			check("Relay cert is chain (not bare leaf)", func() error {
				return checkChainCert(relayCfg.TLS.CertFile)
			})

			// Check listener
			check("Agent listener responding", func() error {
				parts := strings.SplitN(relayCfg.Server.ListenAddr, ":", 2)
				addr := "localhost:" + parts[len(parts)-1]
				conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
				if err != nil {
					return fmt.Errorf("cannot connect to %s", addr)
				}
				conn.Close()
				return nil
			})

			// Admin endpoint
			check("Admin/metrics endpoint", func() error {
				url := fmt.Sprintf("http://%s/healthz", relayCfg.Server.AdminAddr)
				client := &http.Client{Timeout: 3 * time.Second}
				resp, err := client.Get(url)
				if err != nil {
					return fmt.Errorf("cannot reach %s: %w", url, err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					return fmt.Errorf("%s returned %d", url, resp.StatusCode)
				}
				return nil
			})

			// Check each customer port
			for _, c := range relayCfg.Customers {
				for _, p := range c.Ports {
					portName := fmt.Sprintf("Port %d (%s/%s)", p.Port, c.ID, p.Service)
					check(portName+" listening", func() error {
						listen := p.ListenAddr
						if listen == "" {
							listen = "0.0.0.0"
						}
						addr := fmt.Sprintf("localhost:%d", p.Port)
						conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
						if err != nil {
							return fmt.Errorf("not listening on %s", addr)
						}
						conn.Close()
						return nil
					})

					if p.ListenAddr == "127.0.0.1" {
						check(portName+" not externally reachable", func() error {
							// This can only truly be tested from outside, so just warn.
							return fmt.Errorf("WARN: verify externally that port %d is not reachable from the internet", p.Port)
						})
					}
				}
			}
		}

		// Systemd service
		check("atlax-relay service active", func() error {
			return checkSystemdService("atlax-relay")
		})
	}

	// --- Agent checks ---
	if nodeType == 1 || nodeType == 2 {
		tui.Header("Agent Health")

		configPath := tui.Ask("Agent config path", "/etc/atlax/agent.yaml")
		agentCfg, cfgErr := config.ReadAgentConfig(configPath)

		check("Config file readable", func() error {
			if cfgErr != nil {
				return cfgErr
			}
			return nil
		})

		if agentCfg != nil {
			check("Config valid (has services)", func() error {
				if len(agentCfg.Services) == 0 {
					return fmt.Errorf("WARN: no services configured")
				}
				return nil
			})

			check("TLS cert files exist", func() error {
				files := []string{agentCfg.TLS.CertFile, agentCfg.TLS.KeyFile, agentCfg.TLS.CAFile}
				for _, f := range files {
					if _, err := os.Stat(f); err != nil {
						return fmt.Errorf("%s missing", f)
					}
				}
				return nil
			})

			check("Agent cert not expired", func() error {
				return checkCertExpiry(agentCfg.TLS.CertFile, 30)
			})

			check("Agent cert is chain (not bare leaf)", func() error {
				return checkChainCert(agentCfg.TLS.CertFile)
			})

			// Check relay connectivity
			check("Relay reachable", func() error {
				conn, err := net.DialTimeout("tcp", agentCfg.Relay.Addr, 5*time.Second)
				if err != nil {
					return fmt.Errorf("cannot reach relay at %s", agentCfg.Relay.Addr)
				}
				conn.Close()
				return nil
			})

			// Check mTLS handshake
			check("mTLS handshake", func() error {
				return checkMTLS(agentCfg)
			})

			// Check local services
			for _, s := range agentCfg.Services {
				svcName := fmt.Sprintf("Local service %s (%s)", s.Name, s.LocalAddr)
				check(svcName, func() error {
					conn, err := net.DialTimeout("tcp", s.LocalAddr, 3*time.Second)
					if err != nil {
						return fmt.Errorf("cannot connect to %s", s.LocalAddr)
					}
					conn.Close()
					return nil
				})
			}
		}

		check("atlax-agent service active", func() error {
			return checkSystemdService("atlax-agent")
		})
	}

	// --- Summary ---
	tui.Header("Results")
	tui.Table([][]string{
		{"Passed", fmt.Sprintf("%d", passed)},
		{"Warnings", fmt.Sprintf("%d", warned)},
		{"Failed", fmt.Sprintf("%d", failed)},
	})

	if failed > 0 {
		tui.Failf("Health check completed with %d failure(s)", failed)
		return fmt.Errorf("%d check(s) failed", failed)
	}
	if warned > 0 {
		tui.Warnf("Health check passed with %d warning(s)", warned)
	} else {
		tui.Successf("All checks passed")
	}

	return nil
}

// --- helpers ---

func checkCertExpiry(certPath string, thresholdDays int) error {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("invalid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
	if daysLeft <= 0 {
		return fmt.Errorf("EXPIRED %d days ago (CN=%s)", -daysLeft, cert.Subject.CommonName)
	}
	if daysLeft <= thresholdDays {
		return fmt.Errorf("WARN: expires in %d days (CN=%s)", daysLeft, cert.Subject.CommonName)
	}
	return nil
}

func checkChainCert(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count := 0
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("no certificates found in %s", path)
	}
	if count == 1 {
		return fmt.Errorf(
			"%s contains a single certificate (bare leaf). "+
				"atlax requires a chain cert (leaf + intermediate CA). "+
				"Use `ats certs init` or run: cat leaf.crt intermediate.crt > chain.crt",
			path,
		)
	}
	return nil
}

func checkSystemdService(name string) error {
	cmd := exec.Command("systemctl", "is-active", name)
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil || status != "active" {
		return fmt.Errorf("service %s is %s", name, status)
	}
	return nil
}

func checkMTLS(agentCfg *config.AgentConfig) error {
	certData, err := os.ReadFile(agentCfg.TLS.CertFile)
	if err != nil {
		return fmt.Errorf("cannot read agent cert: %w", err)
	}
	keyData, err := os.ReadFile(agentCfg.TLS.KeyFile)
	if err != nil {
		return fmt.Errorf("cannot read agent key: %w", err)
	}
	caData, err := os.ReadFile(agentCfg.TLS.CAFile)
	if err != nil {
		return fmt.Errorf("cannot read CA cert: %w", err)
	}

	cert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return fmt.Errorf("invalid cert/key pair: %w", err)
	}

	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caData)

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   agentCfg.Relay.ServerName,
		MinVersion:   tls.VersionTLS13,
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", agentCfg.Relay.Addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("TLS handshake failed: %w", err)
	}
	conn.Close()

	state := conn.ConnectionState()
	_ = filepath.Base(agentCfg.TLS.CertFile) // suppress unused
	if state.Version < tls.VersionTLS13 {
		return fmt.Errorf("WARN: negotiated TLS %x, expected 1.3", state.Version)
	}
	return nil
}
