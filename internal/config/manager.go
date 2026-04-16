// Package config handles reading, writing, and merging atlax YAML
// configuration files (relay.yaml / agent.yaml).
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// --- Relay config structures ---

type RelayConfig struct {
	Server    RelayServer    `yaml:"server"`
	TLS       RelayTLS       `yaml:"tls"`
	Customers []Customer     `yaml:"customers"`
	Logging   LoggingConfig  `yaml:"logging"`
	Metrics   *MetricsConfig `yaml:"metrics,omitempty"`
}

type RelayServer struct {
	ListenAddr          string `yaml:"listen_addr"`
	AdminAddr           string `yaml:"admin_addr"`
	AdminSocket         string `yaml:"admin_socket"`
	AgentListenAddr     string `yaml:"agent_listen_addr"`
	MaxAgents           int    `yaml:"max_agents"`
	MaxStreamsPerAgent  int    `yaml:"max_streams_per_agent"`
	IdleTimeout         string `yaml:"idle_timeout"`
	ShutdownGracePeriod string `yaml:"shutdown_grace_period"`
	StorePath           string `yaml:"store_path"`
}

type RelayTLS struct {
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	CAFile       string `yaml:"ca_file"`
	ClientCAFile string `yaml:"client_ca_file"`
}

type Customer struct {
	ID             string       `yaml:"id"`
	MaxConnections int          `yaml:"max_connections,omitempty"`
	MaxStreams     int          `yaml:"max_streams,omitempty"`
	Ports          []PortConfig `yaml:"ports"`
}

type PortConfig struct {
	Port        int    `yaml:"port"`
	Service     string `yaml:"service"`
	Description string `yaml:"description,omitempty"`
	ListenAddr  string `yaml:"listen_addr,omitempty"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path,omitempty"`
	Prefix  string `yaml:"prefix,omitempty"`
}

// --- Agent config structures ---

type AgentConfig struct {
	Relay    AgentRelay      `yaml:"relay"`
	TLS      AgentTLS        `yaml:"tls"`
	Services []ServiceConfig `yaml:"services"`
	Logging  LoggingConfig   `yaml:"logging"`
	Update   *UpdateConfig   `yaml:"update,omitempty"`
}

type AgentRelay struct {
	Addr                string `yaml:"addr"`
	ServerName          string `yaml:"server_name"`
	ReconnectInterval   string `yaml:"reconnect_interval,omitempty"`
	ReconnectMaxBackoff string `yaml:"reconnect_max_backoff,omitempty"`
	ReconnectJitter     bool   `yaml:"reconnect_jitter,omitempty"`
	KeepaliveInterval   string `yaml:"keepalive_interval"`
	KeepaliveTimeout    string `yaml:"keepalive_timeout"`
}

type AgentTLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type ServiceConfig struct {
	Name        string `yaml:"name"`
	LocalAddr   string `yaml:"local_addr"`
	Protocol    string `yaml:"protocol"`
	Description string `yaml:"description,omitempty"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type UpdateConfig struct {
	Enabled       bool   `yaml:"enabled"`
	CheckInterval string `yaml:"check_interval,omitempty"`
	ManifestURL   string `yaml:"manifest_url,omitempty"`
}

// --- Read / Write ---

// ReadRelayConfig loads a relay config from disk.
func ReadRelayConfig(path string) (*RelayConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read relay config: %w", err)
	}
	var cfg RelayConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse relay config: %w", err)
	}
	return &cfg, nil
}

// WriteRelayConfig writes a relay config to disk.
func WriteRelayConfig(path string, cfg *RelayConfig) error {
	return writeYAML(path, cfg)
}

// ReadAgentConfig loads an agent config from disk.
func ReadAgentConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read agent config: %w", err)
	}
	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse agent config: %w", err)
	}
	return &cfg, nil
}

// WriteAgentConfig writes an agent config to disk.
func WriteAgentConfig(path string, cfg *AgentConfig) error {
	return writeYAML(path, cfg)
}

// --- Mutation helpers ---

// AddCustomer adds a customer to the relay config. Returns error if ID exists.
func AddCustomer(cfg *RelayConfig, cust Customer) error {
	for _, c := range cfg.Customers {
		if c.ID == cust.ID {
			return fmt.Errorf("customer %q already exists", cust.ID)
		}
	}
	cfg.Customers = append(cfg.Customers, cust)
	return nil
}

// AddPort adds a port to a customer. Returns error if port or service is taken.
func AddPort(cfg *RelayConfig, customerID string, port PortConfig) error {
	for i, c := range cfg.Customers {
		if c.ID == customerID {
			// Check port conflicts across ALL customers.
			for _, cust := range cfg.Customers {
				for _, p := range cust.Ports {
					if p.Port == port.Port {
						return fmt.Errorf("port %d already allocated to customer %q service %q",
							port.Port, cust.ID, p.Service)
					}
				}
			}
			// Check service name unique within customer.
			for _, p := range c.Ports {
				if p.Service == port.Service {
					return fmt.Errorf("service %q already exists for customer %q on port %d",
						port.Service, customerID, p.Port)
				}
			}
			cfg.Customers[i].Ports = append(cfg.Customers[i].Ports, port)
			return nil
		}
	}
	return fmt.Errorf("customer %q not found", customerID)
}

// AddService adds a service to the agent config. Returns error if name exists.
func AddService(cfg *AgentConfig, svc ServiceConfig) error {
	for _, s := range cfg.Services {
		if s.Name == svc.Name {
			return fmt.Errorf("service %q already exists", svc.Name)
		}
	}
	cfg.Services = append(cfg.Services, svc)
	return nil
}

// FindNextPort returns the next unallocated port in the given range.
func FindNextPort(cfg *RelayConfig, rangeStart, rangeEnd int) (int, error) {
	used := make(map[int]bool)
	for _, c := range cfg.Customers {
		for _, p := range c.Ports {
			used[p.Port] = true
		}
	}
	for port := rangeStart; port <= rangeEnd; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports in range %d-%d", rangeStart, rangeEnd)
}

// CustomerByID returns a customer by ID, or nil if not found.
func CustomerByID(cfg *RelayConfig, id string) *Customer {
	for i := range cfg.Customers {
		if cfg.Customers[i].ID == id {
			return &cfg.Customers[i]
		}
	}
	return nil
}

// ListCustomerIDs returns all customer IDs in the config.
func ListCustomerIDs(cfg *RelayConfig) []string {
	ids := make([]string, len(cfg.Customers))
	for i, c := range cfg.Customers {
		ids[i] = c.ID
	}
	return ids
}

// --- Backup ---

// BackupFile creates a timestamped backup of a file.
func BackupFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	backupPath := fmt.Sprintf("%s.bak%s", base, ext)
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return "", err
	}
	return backupPath, nil
}

// --- Defaults ---

// DefaultRelayConfig returns a relay config with sensible defaults.
func DefaultRelayConfig() *RelayConfig {
	return &RelayConfig{
		Server: RelayServer{
			ListenAddr:          "0.0.0.0:8443",
			AdminAddr:           "127.0.0.1:9090",
			MaxAgents:           100,
			MaxStreamsPerAgent:  100,
			IdleTimeout:         "300s",
			ShutdownGracePeriod: "30s",
		},
		TLS: RelayTLS{
			CertFile:     "./certs/relay.crt",
			KeyFile:      "./certs/relay.key",
			CAFile:       "./certs/root-ca.crt",
			ClientCAFile: "./certs/customer-ca.crt",
		},
		Logging: LoggingConfig{Level: "info", Format: "json"},
		Metrics: &MetricsConfig{Enabled: true, Path: "/metrics", Prefix: "atlax"},
	}
}

// DefaultAgentConfig returns an agent config with sensible defaults.
func DefaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		Relay: AgentRelay{
			KeepaliveInterval: "30s",
			KeepaliveTimeout:  "10s",
		},
		TLS: AgentTLS{
			CertFile: "./certs/agent.crt",
			KeyFile:  "./certs/agent.key",
			CAFile:   "./certs/relay-ca.crt",
		},
		Logging: LoggingConfig{Level: "info", Format: "json"},
	}
}

// SupportedServices returns the list of service types atlax supports.
func SupportedServices() []string {
	return []string{
		"http",
		"https",
		"tcp",
		"smb",
		"ssh",
		"ftp",
		"mysql",
		"postgres",
		"redis",
		"mongodb",
		"api",
		"grpc",
		"websocket",
		"custom",
	}
}

// --- internal ---

func writeYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}
	return nil
}
