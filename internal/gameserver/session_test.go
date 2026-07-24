package gameserver

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/tianyu150/mir-eternal/internal/gamestore"
)

func TestSessionTicketLogin(t *testing.T) {
	store, err := gamestore.Open(filepath.Join(t.TempDir(), "game.json"))
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.MaxPacketBytes = 1024
	server := New(config, store, &Stats{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.tickets.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	if err := server.tickets.Add("ULS21-test", "Player1", time.Unix(1_700_000_060, 0)); err != nil {
		t.Fatal(err)
	}

	serverConn, clientConn := net.Pipe()
	client := newSession(server, serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { client.run(ctx); close(done) }()

	login := make([]byte, 162)
	binary.LittleEndian.PutUint16(login, 1001)
	copy(login[72:110], "ULS21-test")
	copy(login[136:153], "00:11:22:33:44")
	if _, err := clientConn.Write(login); err != nil {
		t.Fatal(err)
	}
	// 1002(765), 1012(3), 692(16), 693(6), 1004(849).
	response := make([]byte, 1639)
	if _, err := io.ReadFull(clientConn, response); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		offset int
		id     uint16
	}{{0, 1002}, {765, 1012}, {768, 692}, {784, 693}, {790, 1004}} {
		if got := binary.LittleEndian.Uint16(response[check.offset:]); got != check.id {
			t.Fatalf("packet at %d = %d, want %d", check.offset, got, check.id)
		}
	}
	if _, err := server.tickets.Consume("ULS21-test"); err == nil {
		t.Fatal("login ticket was reusable")
	}
	_ = clientConn.Close()
	<-done
	if client.stage != selectingCharacter || client.account != "Player1" {
		t.Fatalf("session was not authenticated: %+v", client)
	}
}
