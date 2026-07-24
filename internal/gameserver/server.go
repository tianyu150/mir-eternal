package gameserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tianyu150/mir-eternal/internal/gamestore"
	"github.com/tianyu150/mir-eternal/internal/ticket"
)

type Server struct {
	config     Config
	store      *gamestore.Store
	codec      ticket.Codec
	tickets    *ticketStore
	stats      *Stats
	logger     *slog.Logger
	allowed    []*net.IPNet
	mu         sync.Mutex
	sessions   map[*session]struct{}
	online     map[string]*session
	listener   net.Listener
	ticketConn *net.UDPConn
	wg         sync.WaitGroup
}

func New(config Config, store *gamestore.Store, stats *Stats, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{config: config, store: store, codec: ticket.Codec{Secret: []byte(config.TicketSecret)}, tickets: newTicketStore(config.MaxTickets), stats: stats, logger: logger, allowed: config.allowedNetworks(), sessions: make(map[*session]struct{}), online: make(map[string]*session)}
}
func (s *Server) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.config.Listen)
	if err != nil {
		return fmt.Errorf("listen for game clients: %w", err)
	}
	ticketAddress, err := net.ResolveUDPAddr("udp", s.config.TicketListen)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("resolve ticket listen address: %w", err)
	}
	ticketConn, err := net.ListenUDP("udp", ticketAddress)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("listen for tickets: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.ticketConn = ticketConn
	s.mu.Unlock()
	s.logger.Info("game server listening", "network", "tcp", "address", listener.Addr())
	s.logger.Info("ticket receiver listening", "network", "udp", "address", ticketConn.LocalAddr(), "authenticated", len(s.codec.Secret) > 0)
	errCh := make(chan error, 2)
	s.wg.Add(2)
	go func() { defer s.wg.Done(); errCh <- s.acceptLoop(ctx, listener) }()
	go func() { defer s.wg.Done(); errCh <- s.ticketLoop(ctx, ticketConn) }()
	var result error
	select {
	case <-ctx.Done():
	case result = <-errCh:
	}
	_ = listener.Close()
	_ = ticketConn.Close()
	s.closeSessions()
	s.wg.Wait()
	if result != nil && !errors.Is(result, net.ErrClosed) && ctx.Err() == nil {
		return result
	}
	return nil
}
func (s *Server) acceptLoop(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			_ = conn.Close()
			return nil
		}
		s.mu.Lock()
		full := len(s.sessions) >= s.config.MaxConnections
		s.mu.Unlock()
		if full {
			s.stats.rejected.Add(1)
			_ = conn.Close()
			continue
		}
		client := newSession(s, conn)
		s.mu.Lock()
		s.sessions[client] = struct{}{}
		s.mu.Unlock()
		s.stats.connections.Add(1)
		s.wg.Add(1)
		go func() { defer s.wg.Done(); client.run(ctx); s.removeSession(client) }()
	}
}
func (s *Server) ticketLoop(ctx context.Context, conn *net.UDPConn) error {
	buffer := make([]byte, 1025)
	for {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		if n > 1024 || !s.ticketSourceAllowed(remote.IP) {
			s.stats.rejected.Add(1)
			continue
		}
		message, err := s.codec.Decode(buffer[:n])
		if err != nil {
			s.stats.rejected.Add(1)
			s.logger.Warn("rejected login ticket", "remote", remote, "error", err)
			continue
		}
		expires := message.ExpiresAt
		if expires.IsZero() {
			expires = time.Now().Add(s.config.TicketTTL)
		}
		if err := s.tickets.Add(message.Ticket, message.Account, expires); err != nil {
			s.stats.rejected.Add(1)
			s.logger.Warn("login ticket not stored", "error", err)
			continue
		}
		s.stats.tickets.Add(1)
	}
}
func (s *Server) ticketSourceAllowed(ip net.IP) bool {
	if len(s.allowed) == 0 {
		return true
	}
	for _, network := range s.allowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Server) removeSession(client *session) {
	client.close()
	s.mu.Lock()
	delete(s.sessions, client)
	if client.account != "" && s.online[strings.ToLower(client.account)] == client {
		delete(s.online, strings.ToLower(client.account))
		s.stats.authenticated.Add(-1)
	}
	s.mu.Unlock()
	s.stats.connections.Add(-1)
}
func (s *Server) claimAccount(client *session, account string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(account)
	if old := s.online[key]; old != nil && old != client {
		go func() { packet, _ := loginErrorPacket(260, 0, 0); _ = old.writePackets(packet); old.close() }()
		return false
	}
	s.online[key] = client
	s.stats.authenticated.Add(1)
	return true
}
func (s *Server) closeSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for client := range s.sessions {
		client.close()
	}
}
func (s *Server) Snapshot() StatsSnapshot {
	accounts, characters := s.store.Counts()
	return StatsSnapshot{Connections: s.stats.connections.Load(), Authenticated: s.stats.authenticated.Load(), Tickets: s.stats.tickets.Load(), BytesReceived: s.stats.bytesReceived.Load(), BytesSent: s.stats.bytesSent.Load(), PacketsReceived: s.stats.packetsReceived.Load(), PacketsSent: s.stats.packetsSent.Load(), UnhandledPackets: s.stats.unhandledPackets.Load(), Rejected: s.stats.rejected.Load(), PendingTickets: s.tickets.Len(), Accounts: accounts, Characters: characters}
}
