package accountserver

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tianyu150/mir-eternal/internal/account"
	"github.com/tianyu150/mir-eternal/internal/ticket"
)

type handlerRepository struct{ accounts map[string]account.Account }

func (r *handlerRepository) Get(_ context.Context, name string) (account.Account, error) {
	a, ok := r.accounts[strings.ToLower(name)]
	if !ok {
		return account.Account{}, account.ErrNotFound
	}
	return a, nil
}
func (r *handlerRepository) Exists(_ context.Context, name string) (bool, error) {
	_, ok := r.accounts[strings.ToLower(name)]
	return ok, nil
}
func (r *handlerRepository) Create(_ context.Context, a account.Account) error {
	key := strings.ToLower(a.Name)
	if _, ok := r.accounts[key]; ok {
		return account.ErrExists
	}
	r.accounts[key] = a
	return nil
}
func (r *handlerRepository) UpdatePassword(_ context.Context, name, password string, encrypted bool) error {
	a, ok := r.accounts[strings.ToLower(name)]
	if !ok {
		return account.ErrNotFound
	}
	a.Password, a.PasswordEncrypted = password, encrypted
	r.accounts[strings.ToLower(name)] = a
	return nil
}
func (r *handlerRepository) Count(context.Context) (int, error) { return len(r.accounts), nil }

type captureTickets struct {
	server GameServer
	data   []byte
}

func (s *captureTickets) SendTicket(_ context.Context, server GameServer, data []byte) error {
	s.server, s.data = server, append([]byte(nil), data...)
	return nil
}

func TestHandlerProtocolFlow(t *testing.T) {
	repo := &handlerRepository{accounts: make(map[string]account.Account)}
	accounts := account.NewService(repo, 4)
	if _, err := accounts.Register(context.Background(), "Player1", "secret1", "pet", "cat"); err != nil {
		t.Fatal(err)
	}
	capture := &captureTickets{}
	stats := &Stats{}
	h := NewHandler(accounts, []GameServer{{Name: "Test", PublicAddress: "127.0.0.1:8701", TicketAddress: "127.0.0.1:6678"}}, ticket.Codec{}, 5*time.Minute, capture, stats, slog.New(slog.NewTextHandler(discardWriter{}, nil)))

	if got := string(h.Handle(context.Background(), []byte("1 0 Player1 wrong"))); got != "1 1 wrong user name or password" {
		t.Fatalf("login failure = %q", got)
	}
	if got := string(h.Handle(context.Background(), []byte("2 0 Player1 secret1"))); got != "2 0 Player1 secret1 127.0.0.1,8701/Test" {
		t.Fatalf("login = %q", got)
	}
	got := string(h.Handle(context.Background(), []byte("3 3 Player1 secret1 Test")))
	parts := strings.Fields(got)
	if len(parts) != 5 || parts[1] != "6" || !strings.HasPrefix(parts[4], "ULS21-") {
		t.Fatalf("ticket response = %q", got)
	}
	if capture.server.Name != "Test" || !strings.HasSuffix(string(capture.data), ";Player1") {
		t.Fatalf("ticket datagram = %q to %+v", capture.data, capture.server)
	}
}

func TestHandlerRejectsTruncatedPackets(t *testing.T) {
	h := NewHandler(account.NewService(&handlerRepository{accounts: make(map[string]account.Account)}, 4), []GameServer{{Name: "Test", PublicAddress: "127.0.0.1:8701", TicketAddress: "127.0.0.1:6678"}}, ticket.Codec{}, time.Minute, &captureTickets{}, &Stats{}, nil)
	for _, packet := range []string{"", "x", "1 x", "1 0", "1 1 name", "1 2 name pass", "1 3 name pass"} {
		_ = h.Handle(context.Background(), []byte(packet)) // Must never panic.
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
