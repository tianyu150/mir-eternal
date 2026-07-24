// Package accountserver implements the legacy launcher UDP protocol as a
// headless, cross-platform Go service.
package accountserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type GameServer struct {
	Name          string `json:"name"`
	PublicAddress string `json:"public_address"`
	TicketAddress string `json:"ticket_address"`
}

type Config struct {
	Listen            string        `json:"listen"`
	AccountsDirectory string        `json:"accounts_directory"`
	LegacyServersFile string        `json:"legacy_servers_file"`
	TicketPort        int           `json:"ticket_port"`
	TicketTTL         time.Duration `json:"-"`
	TicketTTLText     string        `json:"ticket_ttl"`
	TicketSecret      string        `json:"ticket_secret"`
	MaxDatagramBytes  int           `json:"max_datagram_bytes"`
	Workers           int           `json:"workers"`
	AdminListen       string        `json:"admin_listen"`
	BCryptCost        int           `json:"bcrypt_cost"`
	Servers           []GameServer  `json:"servers"`
}

func DefaultConfig() Config {
	return Config{
		Listen: ":7000", AccountsDirectory: "data/accounts", LegacyServersFile: "server",
		TicketPort: 6678, TicketTTL: 5 * time.Minute, TicketTTLText: "5m",
		MaxDatagramBytes: 1024, Workers: 64, AdminListen: "127.0.0.1:9100",
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open account config: %w", err)
		}
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&cfg)
		_ = file.Close()
		if err != nil {
			return Config{}, fmt.Errorf("decode account config: %w", err)
		}
	}
	if cfg.TicketTTLText != "" {
		ttl, err := time.ParseDuration(cfg.TicketTTLText)
		if err != nil {
			return Config{}, fmt.Errorf("ticket_ttl: %w", err)
		}
		cfg.TicketTTL = ttl
	}
	if secret := os.Getenv("MIR_TICKET_SECRET"); secret != "" {
		cfg.TicketSecret = secret
	}
	if len(cfg.Servers) == 0 && cfg.LegacyServersFile != "" {
		servers, err := LoadLegacyServers(cfg.LegacyServersFile, cfg.TicketPort)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
		cfg.Servers = servers
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen is required")
	}
	if c.AccountsDirectory == "" {
		return errors.New("accounts_directory is required")
	}
	if c.MaxDatagramBytes < 64 || c.MaxDatagramBytes > 64*1024 {
		return errors.New("max_datagram_bytes must be between 64 and 65536")
	}
	if c.Workers < 1 {
		return errors.New("workers must be positive")
	}
	if c.TicketTTL <= 0 {
		return errors.New("ticket_ttl must be positive")
	}
	if len(c.Servers) == 0 {
		return errors.New("at least one game server is required")
	}
	seen := make(map[string]struct{}, len(c.Servers))
	for i, server := range c.Servers {
		if server.Name == "" || server.PublicAddress == "" || server.TicketAddress == "" {
			return fmt.Errorf("servers[%d]: name, public_address and ticket_address are required", i)
		}
		if _, ok := seen[server.Name]; ok {
			return fmt.Errorf("duplicate game server %q", server.Name)
		}
		seen[server.Name] = struct{}{}
		if _, _, err := net.SplitHostPort(server.PublicAddress); err != nil {
			return fmt.Errorf("servers[%d].public_address: %w", i, err)
		}
		if _, _, err := net.SplitHostPort(server.TicketAddress); err != nil {
			return fmt.Errorf("servers[%d].ticket_address: %w", i, err)
		}
	}
	return nil
}

// LoadLegacyServers parses the C# "server" file: host,game-port/name. The
// internal ticket endpoint uses ticketPort, matching Settings.Default.TSPort.
func LoadLegacyServers(path string, ticketPort int) ([]GameServer, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open legacy servers file: %w", err)
	}
	defer file.Close()
	var servers []GameServer
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		value := strings.TrimSpace(scanner.Text())
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		slash := strings.LastIndex(value, "/")
		comma := strings.LastIndex(value[:max(slash, 0)], ",")
		if slash <= 0 || comma <= 0 || slash == len(value)-1 {
			return nil, fmt.Errorf("legacy servers file line %d: expected host,port/name", line)
		}
		host, port, name := value[:comma], value[comma+1:slash], value[slash+1:]
		if _, err := strconv.Atoi(port); err != nil {
			return nil, fmt.Errorf("legacy servers file line %d: invalid port", line)
		}
		servers = append(servers, GameServer{Name: name, PublicAddress: net.JoinHostPort(host, port), TicketAddress: net.JoinHostPort(host, strconv.Itoa(ticketPort))})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read legacy servers file: %w", err)
	}
	return servers, nil
}
