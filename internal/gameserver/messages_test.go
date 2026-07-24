package gameserver

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/tianyu150/mir-eternal/internal/gameprotocol"
	"github.com/tianyu150/mir-eternal/internal/gamestore"
)

func TestLegacyLoginMessages(t *testing.T) {
	packet, err := loginSuccessPacket()
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 765 || binary.LittleEndian.Uint16(packet) != 1002 || binary.LittleEndian.Uint16(packet[2:4]) != 765 {
		t.Fatalf("login packet header: len=%d bytes=%x", len(packet), packet[:4])
	}
	if len(agreement) != 761 {
		t.Fatalf("agreement length=%d", len(agreement))
	}
}
func TestCharacterListMessage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	characters := []gamestore.Character{{ID: 7, Name: "Hero", Race: 2, Gender: 1, Hair: 3, HairColor: 4, Face: 5, Level: 6, MapID: 142, Status: gamestore.Active, OfflineAt: now}}
	wire, err := characterListPacket(characters)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) != 849 {
		t.Fatalf("packet length=%d", len(wire))
	}
	for i := 4; i < len(wire); i++ {
		wire[i] ^= gameprotocol.EncryptionKey
	}
	if wire[2] != 1 || binary.LittleEndian.Uint32(wire[3:7]) != 7 || string(wire[7:11]) != "Hero" {
		t.Fatalf("unexpected character list prefix: %x", wire[:20])
	}
}
