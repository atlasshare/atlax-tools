package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Round-trip helpers ---

func sampleRelayConfig() *RelayConfig {
	return &RelayConfig{
		Server: RelayServer{
			ListenAddr:          "0.0.0.0:8443",
			AdminAddr:           "127.0.0.1:9090",
			MaxAgents:           42,
			MaxStreamsPerAgent:  100,
			IdleTimeout:         "60s",
			ShutdownGracePeriod: "15s",
		},
		TLS: RelayTLS{
			CertFile:     "/etc/atlax/relay.crt",
			KeyFile:      "/etc/atlax/relay.key",
			CAFile:       "/etc/atlax/root-ca.crt",
			ClientCAFile: "/etc/atlax/customer-ca.crt",
		},
		Customers: []Customer{
			{
				ID:             "customer-a",
				MaxConnections: 10,
				MaxStreams:     50,
				Ports: []PortConfig{
					{Port: 18080, Service: "web", Description: "Dashboard", ListenAddr: "0.0.0.0"},
					{Port: 18445, Service: "smb"},
				},
			},
		},
		Logging: LoggingConfig{Level: "debug", Format: "json"},
		Metrics: &MetricsConfig{Enabled: true, Path: "/metrics", Prefix: "atlax"},
	}
}

func sampleAgentConfig() *AgentConfig {
	return &AgentConfig{
		Relay: AgentRelay{
			Addr:              "relay.example.com:8443",
			ServerName:        "relay.example.com",
			ReconnectInterval: "5s",
			KeepaliveInterval: "30s",
			KeepaliveTimeout:  "10s",
		},
		TLS: AgentTLS{
			CertFile: "/etc/atlax/agent.crt",
			KeyFile:  "/etc/atlax/agent.key",
			CAFile:   "/etc/atlax/relay-ca.crt",
		},
		Services: []ServiceConfig{
			{Name: "web", LocalAddr: "localhost:3000", Protocol: "http", Description: "app"},
			{Name: "smb", LocalAddr: "localhost:445", Protocol: "tcp"},
		},
		Logging: LoggingConfig{Level: "info", Format: "json"},
		Update: &UpdateConfig{
			Enabled:       true,
			CheckInterval: "6h",
			ManifestURL:   "https://updates.example.com/manifest.json",
		},
	}
}

// --- RelayConfig round-trip ---

func TestRelayConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "relay.yaml")
	want := sampleRelayConfig()

	require.NoError(t, WriteRelayConfig(path, want))

	got, err := ReadRelayConfig(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestWriteRelayConfig_CreatesParentDirectory(t *testing.T) {
	t.Parallel()
	// Parent subdir does not yet exist.
	path := filepath.Join(t.TempDir(), "nested", "sub", "relay.yaml")
	require.NoError(t, WriteRelayConfig(path, DefaultRelayConfig()))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())
}

func TestReadRelayConfig_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := ReadRelayConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read relay config")
}

func TestReadRelayConfig_InvalidYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server: [this is not: valid yaml"), 0o644))

	_, err := ReadRelayConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse relay config")
}

// --- AgentConfig round-trip ---

func TestAgentConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	want := sampleAgentConfig()

	require.NoError(t, WriteAgentConfig(path, want))

	got, err := ReadAgentConfig(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestReadAgentConfig_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := ReadAgentConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read agent config")
}

func TestReadAgentConfig_InvalidYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("relay: [this is: broken"), 0o644))

	_, err := ReadAgentConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse agent config")
}

// --- AddCustomer ---

func TestAddCustomer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		seed      []Customer
		toAdd     Customer
		wantErr   bool
		errSubstr string
	}{
		{
			name:  "happy path on empty config",
			seed:  nil,
			toAdd: Customer{ID: "customer-x", Ports: []PortConfig{{Port: 18080, Service: "web"}}},
		},
		{
			name: "appending second distinct customer",
			seed: []Customer{
				{ID: "customer-a"},
			},
			toAdd: Customer{ID: "customer-b"},
		},
		{
			name: "duplicate customer ID rejected",
			seed: []Customer{
				{ID: "customer-dup"},
			},
			toAdd:     Customer{ID: "customer-dup"},
			wantErr:   true,
			errSubstr: `customer "customer-dup" already exists`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &RelayConfig{Customers: tc.seed}
			err := AddCustomer(cfg, tc.toAdd)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				// Seed must remain unchanged on error.
				assert.Len(t, cfg.Customers, len(tc.seed))
				return
			}
			require.NoError(t, err)
			assert.Len(t, cfg.Customers, len(tc.seed)+1)
			assert.Equal(t, tc.toAdd.ID, cfg.Customers[len(cfg.Customers)-1].ID)
		})
	}
}

// --- AddPort ---

func TestAddPort(t *testing.T) {
	t.Parallel()

	baseConfig := func() *RelayConfig {
		return &RelayConfig{
			Customers: []Customer{
				{
					ID: "customer-a",
					Ports: []PortConfig{
						{Port: 18080, Service: "web"},
					},
				},
				{
					ID: "customer-b",
					Ports: []PortConfig{
						{Port: 18445, Service: "smb"},
					},
				},
			},
		}
	}

	cases := []struct {
		name       string
		customerID string
		port       PortConfig
		wantErr    bool
		errSubstr  string
	}{
		{
			name:       "happy path: new port for existing customer",
			customerID: "customer-a",
			port:       PortConfig{Port: 18070, Service: "api"},
		},
		{
			name:       "port conflict: port owned by same customer",
			customerID: "customer-a",
			port:       PortConfig{Port: 18080, Service: "other"},
			wantErr:    true,
			errSubstr:  "port 18080 already allocated",
		},
		{
			name:       "port conflict: port owned by different customer",
			customerID: "customer-a",
			port:       PortConfig{Port: 18445, Service: "other"},
			wantErr:    true,
			errSubstr:  "port 18445 already allocated",
		},
		{
			name:       "duplicate service name within same customer",
			customerID: "customer-a",
			port:       PortConfig{Port: 19000, Service: "web"},
			wantErr:    true,
			errSubstr:  `service "web" already exists for customer "customer-a"`,
		},
		{
			name:       "unknown customer",
			customerID: "customer-ghost",
			port:       PortConfig{Port: 19000, Service: "web"},
			wantErr:    true,
			errSubstr:  `customer "customer-ghost" not found`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			err := AddPort(cfg, tc.customerID, tc.port)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
			cust := CustomerByID(cfg, tc.customerID)
			require.NotNil(t, cust)
			found := false
			for _, p := range cust.Ports {
				if p.Port == tc.port.Port && p.Service == tc.port.Service {
					found = true
					break
				}
			}
			assert.True(t, found, "newly added port should be attached to the target customer")
		})
	}
}

// --- AddService (agent) ---

func TestAddService(t *testing.T) {
	t.Parallel()

	cfg := &AgentConfig{
		Services: []ServiceConfig{
			{Name: "web", LocalAddr: "localhost:3000", Protocol: "http"},
		},
	}

	// Happy path.
	err := AddService(cfg, ServiceConfig{Name: "smb", LocalAddr: "localhost:445", Protocol: "tcp"})
	require.NoError(t, err)
	assert.Len(t, cfg.Services, 2)

	// Duplicate.
	err = AddService(cfg, ServiceConfig{Name: "web", LocalAddr: "localhost:9999", Protocol: "http"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `service "web" already exists`)
	assert.Len(t, cfg.Services, 2, "duplicate must not append")
}

// --- FindNextPort ---

func TestFindNextPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		seed    []Customer
		start   int
		end     int
		want    int
		wantErr bool
	}{
		{
			name:  "empty config returns start of range",
			seed:  nil,
			start: 18000,
			end:   18010,
			want:  18000,
		},
		{
			name: "skips allocated ports",
			seed: []Customer{
				{ID: "a", Ports: []PortConfig{{Port: 18000, Service: "web"}}},
				{ID: "b", Ports: []PortConfig{{Port: 18001, Service: "api"}}},
			},
			start: 18000,
			end:   18010,
			want:  18002,
		},
		{
			name: "skips non-contiguous holes",
			seed: []Customer{
				{ID: "a", Ports: []PortConfig{
					{Port: 18000, Service: "web"},
					{Port: 18002, Service: "smb"},
				}},
			},
			start: 18000,
			end:   18010,
			want:  18001,
		},
		{
			name: "no available ports",
			seed: []Customer{
				{ID: "a", Ports: []PortConfig{
					{Port: 19000, Service: "a"},
					{Port: 19001, Service: "b"},
					{Port: 19002, Service: "c"},
				}},
			},
			start:   19000,
			end:     19002,
			wantErr: true,
		},
		{
			name: "inverted range returns error",
			seed: nil,
			// When end < start, the loop never enters and the function
			// returns the no-available-ports error.
			start:   19000,
			end:     18999,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &RelayConfig{Customers: tc.seed}
			port, err := FindNextPort(cfg, tc.start, tc.end)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "no available ports")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, port)
		})
	}
}

// --- CustomerByID / ListCustomerIDs ---

func TestCustomerByID(t *testing.T) {
	t.Parallel()
	cfg := &RelayConfig{
		Customers: []Customer{
			{ID: "customer-a"},
			{ID: "customer-b"},
		},
	}

	got := CustomerByID(cfg, "customer-b")
	require.NotNil(t, got)
	assert.Equal(t, "customer-b", got.ID)

	// Mutating via pointer must reflect in the config slice — confirms
	// the function returns a reference into the slice, not a copy.
	got.MaxStreams = 99
	assert.Equal(t, 99, cfg.Customers[1].MaxStreams)

	assert.Nil(t, CustomerByID(cfg, "customer-ghost"))
}

func TestListCustomerIDs(t *testing.T) {
	t.Parallel()

	// Empty config.
	ids := ListCustomerIDs(&RelayConfig{})
	assert.Empty(t, ids)

	cfg := &RelayConfig{
		Customers: []Customer{
			{ID: "customer-a"},
			{ID: "customer-b"},
			{ID: "customer-c"},
		},
	}
	ids = ListCustomerIDs(cfg)
	assert.Equal(t, []string{"customer-a", "customer-b", "customer-c"}, ids)
}

// --- BackupFile ---

func TestBackupFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "relay.yaml")
	content := []byte("server:\n  listen_addr: 0.0.0.0:8443\n")
	require.NoError(t, os.WriteFile(src, content, 0o644))

	backupPath, err := BackupFile(src)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "relay.bak.yaml"), backupPath)

	got, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestBackupFile_MissingSourceReturnsError(t *testing.T) {
	t.Parallel()
	_, err := BackupFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
}

// --- Defaults / SupportedServices ---

func TestDefaultRelayConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultRelayConfig()
	require.NotNil(t, cfg)

	assert.Equal(t, "0.0.0.0:8443", cfg.Server.ListenAddr)
	assert.Equal(t, "127.0.0.1:9090", cfg.Server.AdminAddr)
	assert.Equal(t, 100, cfg.Server.MaxAgents)
	assert.Equal(t, 100, cfg.Server.MaxStreamsPerAgent)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
	require.NotNil(t, cfg.Metrics)
	assert.True(t, cfg.Metrics.Enabled)
}

func TestDefaultAgentConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultAgentConfig()
	require.NotNil(t, cfg)

	assert.Equal(t, "30s", cfg.Relay.KeepaliveInterval)
	assert.Equal(t, "10s", cfg.Relay.KeepaliveTimeout)
	assert.NotEmpty(t, cfg.TLS.CertFile)
	assert.Equal(t, "info", cfg.Logging.Level)
}

func TestSupportedServices(t *testing.T) {
	t.Parallel()
	svcs := SupportedServices()
	assert.NotEmpty(t, svcs)
	for _, expected := range []string{"http", "tcp", "smb", "ssh", "postgres", "custom"} {
		assert.Contains(t, svcs, expected)
	}
}
