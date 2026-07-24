package gameserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

type Config struct {
	Listen             string        `json:"listen"`
	TicketListen       string        `json:"ticket_listen"`
	TicketSecret       string        `json:"ticket_secret"`
	TicketTTL          time.Duration `json:"-"`
	TicketTTLText      string        `json:"ticket_ttl"`
	TicketAllowedCIDRs []string      `json:"ticket_allowed_cidrs"`
	DatabasePath       string        `json:"database_path"`
	AdminListen        string        `json:"admin_listen"`
	MaxConnections     int           `json:"max_connections"`
	MaxPacketBytes     uint32        `json:"max_packet_bytes"`
	MaxTickets         int           `json:"max_tickets"`
	LoginTimeout       time.Duration `json:"-"`
	LoginTimeoutText   string        `json:"login_timeout"`
	IdleTimeout        time.Duration `json:"-"`
	IdleTimeoutText    string        `json:"idle_timeout"`
}

func DefaultConfig() Config {
	return Config{Listen: ":8701", TicketListen: ":6678", TicketTTL: 5 * time.Minute, TicketTTLText: "5m", DatabasePath: "data/game/game.json", AdminListen: "127.0.0.1:9101", MaxConnections: 10000, MaxPacketBytes: 1 << 20, MaxTickets: 100000, LoginTimeout: 30 * time.Second, LoginTimeoutText: "30s", IdleTimeout: 5 * time.Minute, IdleTimeoutText: "5m"}
}
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open game config: %w", err)
		}
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&cfg)
		_ = file.Close()
		if err != nil {
			return Config{}, fmt.Errorf("decode game config: %w", err)
		}
	}
	for _, item := range []struct {
		text   string
		target *time.Duration
		name   string
	}{{cfg.TicketTTLText, &cfg.TicketTTL, "ticket_ttl"}, {cfg.LoginTimeoutText, &cfg.LoginTimeout, "login_timeout"}, {cfg.IdleTimeoutText, &cfg.IdleTimeout, "idle_timeout"}} {
		value, err := time.ParseDuration(item.text)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", item.name, err)
		}
		*item.target = value
	}
	if secret := os.Getenv("MIR_TICKET_SECRET"); secret != "" {
		cfg.TicketSecret = secret
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
func (c Config) Validate() error {
	if c.Listen == "" || c.TicketListen == "" {
		return errors.New("listen and ticket_listen are required")
	}
	if c.DatabasePath == "" {
		return errors.New("database_path is required")
	}
	if c.MaxConnections < 1 || c.MaxTickets < 1 {
		return errors.New("max_connections and max_tickets must be positive")
	}
	if c.MaxPacketBytes < 162 || c.MaxPacketBytes > 64<<20 {
		return errors.New("max_packet_bytes must be between 162 and 67108864")
	}
	if c.TicketTTL <= 0 || c.LoginTimeout <= 0 || c.IdleTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	for _, value := range c.TicketAllowedCIDRs {
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("ticket_allowed_cidrs %q: %w", value, err)
		}
	}
	return nil
}
func (c Config) allowedNetworks() []*net.IPNet {
	result := make([]*net.IPNet, 0, len(c.TicketAllowedCIDRs))
	for _, value := range c.TicketAllowedCIDRs {
		_, network, _ := net.ParseCIDR(value)
		result = append(result, network)
	}
	return result
}
