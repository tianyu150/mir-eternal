package accountserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tianyu150/mir-eternal/internal/account"
	"github.com/tianyu150/mir-eternal/internal/ticket"
)

type TicketSender interface {
	SendTicket(context.Context, GameServer, []byte) error
}

type Handler struct {
	accounts *account.Service
	servers  map[string]GameServer
	list     string
	tickets  ticket.Codec
	ttl      time.Duration
	sender   TicketSender
	stats    *Stats
	logger   *slog.Logger
}

func NewHandler(accounts *account.Service, servers []GameServer, codec ticket.Codec, ttl time.Duration, sender TicketSender, stats *Stats, logger *slog.Logger) *Handler {
	serverMap := make(map[string]GameServer, len(servers))
	lines := make([]string, 0, len(servers))
	for _, server := range servers {
		serverMap[server.Name] = server
		host, port, _ := strings.Cut(server.PublicAddress, ":")
		// Preserve IPv6 addresses correctly when producing host,port/name.
		if h, p, err := netSplitHostPort(server.PublicAddress); err == nil {
			host, port = h, p
		}
		lines = append(lines, host+","+port+"/"+server.Name)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{accounts: accounts, servers: serverMap, list: strings.Join(lines, "\n"), tickets: codec, ttl: ttl, sender: sender, stats: stats, logger: logger}
}

// netSplitHostPort is a variable to keep Handler tests independent of DNS.
var netSplitHostPort = func(value string) (string, string, error) {
	// net.SplitHostPort does no network lookup.
	return splitHostPort(value)
}

func (h *Handler) Handle(ctx context.Context, data []byte) []byte {
	parts := strings.Fields(string(data))
	if len(parts) < 2 {
		h.stats.badPackets.Add(1)
		return nil
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		h.stats.badPackets.Add(1)
		return nil
	}
	operation, err := strconv.Atoi(parts[1])
	if err != nil {
		h.stats.badPackets.Add(1)
		return nil
	}
	counter := parts[0]
	switch operation {
	case 0:
		if len(parts) != 4 {
			return h.badRequest(counter)
		}
		ok, err := h.accounts.Authenticate(ctx, parts[2], parts[3])
		if err != nil {
			h.logger.Error("authenticate account", "error", err)
			return []byte(counter + " 1 server error")
		}
		if !ok {
			return []byte(counter + " 1 wrong user name or password")
		}
		h.logger.Info("account login successful", "account", parts[2])
		return []byte(fmt.Sprintf("%s 0 %s %s %s", counter, parts[2], parts[3], h.list))
	case 1:
		if len(parts) != 6 {
			return h.badRequest(counter)
		}
		if err := account.ValidateRegistration(parts[2], parts[3], parts[4], parts[5]); err != nil {
			return []byte(counter + " 3 " + err.Error())
		}
		exists, err := h.accounts.Exists(ctx, parts[2])
		if err != nil {
			h.logger.Error("check account", "error", err)
			return []byte(counter + " 3 server error")
		}
		if exists {
			return []byte(counter + " 3 Username already exists")
		}
		if _, err := h.accounts.Register(ctx, parts[2], parts[3], parts[4], parts[5]); err != nil {
			if errors.Is(err, account.ErrExists) {
				return []byte(counter + " 3 Username already exists")
			}
			h.logger.Error("register account", "error", err)
			return []byte(counter + " 3 server error")
		}
		h.stats.accounts.Add(1)
		h.stats.newAccounts.Add(1)
		h.logger.Info("account created", "account", parts[2])
		return []byte(fmt.Sprintf("%s 2 %s %s", counter, parts[2], parts[3]))
	case 2:
		if len(parts) != 6 {
			return h.badRequest(counter)
		}
		result, err := h.accounts.ResetPassword(ctx, parts[2], parts[3], parts[4], parts[5])
		if err != nil {
			h.logger.Error("reset account password", "error", err)
			return []byte(counter + " 5 2")
		}
		if result != account.ResetSuccess {
			return []byte(fmt.Sprintf("%s 5 %d", counter, result))
		}
		h.logger.Info("account password changed", "account", parts[2])
		return []byte(fmt.Sprintf("%s 4 %s %s", counter, parts[2], parts[3]))
	case 3:
		if len(parts) != 5 {
			return h.badRequest(counter)
		}
		ok, err := h.accounts.Authenticate(ctx, parts[2], parts[3])
		if err != nil {
			h.logger.Error("authenticate ticket request", "error", err)
			return []byte(counter + " 7 server error")
		}
		if !ok {
			return []byte(counter + " 7 wrong user name or password")
		}
		server, ok := h.servers[parts[4]]
		if !ok {
			return []byte(counter + " 7 server not found")
		}
		value, err := ticket.Generate()
		if err != nil {
			h.logger.Error("generate ticket", "error", err)
			return []byte(counter + " 7 server error")
		}
		message, err := h.tickets.Encode(value, parts[2], time.Now().Add(h.ttl))
		if err != nil {
			h.logger.Error("encode ticket", "error", err)
			return []byte(counter + " 7 server error")
		}
		if err := h.sender.SendTicket(ctx, server, message); err != nil {
			h.logger.Error("send ticket", "server", server.Name, "error", err)
			return []byte(counter + " 7 server unavailable")
		}
		h.stats.tickets.Add(1)
		h.logger.Info("login ticket generated", "account", parts[2], "server", server.Name)
		return []byte(fmt.Sprintf("%s 6 %s %s %s", counter, parts[2], parts[3], value))
	default:
		h.stats.badPackets.Add(1)
		return nil
	}
}

func (h *Handler) badRequest(counter string) []byte {
	h.stats.badPackets.Add(1)
	return []byte(counter + " 9 bad packet")
}
