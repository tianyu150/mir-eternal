package accountserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
)

type Server struct {
	config  Config
	handler *Handler
	stats   *Stats
	logger  *slog.Logger

	mu   sync.Mutex
	conn *net.UDPConn
}

func New(config Config, handler *Handler, stats *Stats, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{config: config, handler: handler, stats: stats, logger: logger}
}

func (s *Server) Serve(ctx context.Context) error {
	address, err := net.ResolveUDPAddr("udp", s.config.Listen)
	if err != nil {
		return fmt.Errorf("resolve account listen address: %w", err)
	}
	conn, err := net.ListenUDP("udp", address)
	if err != nil {
		return fmt.Errorf("listen for launcher datagrams: %w", err)
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.conn = nil; s.mu.Unlock(); _ = conn.Close() }()
	go func() { <-ctx.Done(); _ = conn.Close() }()
	s.logger.Info("account server listening", "network", "udp", "address", conn.LocalAddr())

	sem := make(chan struct{}, s.config.Workers)
	var workers sync.WaitGroup
	defer workers.Wait()
	buffer := make([]byte, s.config.MaxDatagramBytes+1)
	for {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read launcher datagram: %w", err)
		}
		if n > s.config.MaxDatagramBytes {
			s.stats.badPackets.Add(1)
			s.logger.Warn("oversized launcher datagram", "remote", remote, "bytes", n)
			continue
		}
		s.stats.bytesReceived.Add(int64(n))
		request := append([]byte(nil), buffer[:n]...)
		select {
		case sem <- struct{}{}:
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-sem }()
				response := s.handler.Handle(ctx, request)
				if len(response) == 0 {
					return
				}
				if _, err := conn.WriteToUDP(response, remote); err != nil {
					s.logger.Warn("send launcher response", "remote", remote, "error", err)
					return
				}
				s.stats.bytesSent.Add(int64(len(response)))
			}()
		default:
			s.stats.badPackets.Add(1)
			s.logger.Warn("account worker pool full", "remote", remote)
		}
	}
}

func (s *Server) SendTicket(ctx context.Context, game GameServer, message []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	address, err := net.ResolveUDPAddr("udp", game.TicketAddress)
	if err != nil {
		return fmt.Errorf("resolve ticket address: %w", err)
	}
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errors.New("account server is not listening")
	}
	if _, err := conn.WriteToUDP(message, address); err != nil {
		return fmt.Errorf("send ticket datagram: %w", err)
	}
	s.stats.bytesSent.Add(int64(len(message)))
	return nil
}
