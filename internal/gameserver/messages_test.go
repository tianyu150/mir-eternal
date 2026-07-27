package gameserver

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/tianyu150/mir-eternal/internal/gameprotocol"
	"github.com/tianyu150/mir-eternal/internal/gamestore"
	"github.com/tianyu150/mir-eternal/internal/gameworld"
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

func decryptPacket(packet []byte) []byte {
	decoded := append([]byte(nil), packet...)
	for index := 4; index < len(decoded); index++ {
		decoded[index] ^= gameprotocol.EncryptionKey
	}
	return decoded
}

func TestGuardPacketsPreserveLegacyLayout(t *testing.T) {
	guard := gameworld.Guard{ObjectID: 1_500_000_001, Template: 6734, Level: 99, Position: gameworld.Point{X: 935, Y: 375}, Altitude: 10, Direction: 7168, MaxHP: 9999}
	packets, err := guardVisiblePackets(guard)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 3 {
		t.Fatalf("guard visibility packet count=%d", len(packets))
	}
	stop := decryptPacket(packets[0])
	visible := decryptPacket(packets[1])
	hp := decryptPacket(packets[2])
	if binary.LittleEndian.Uint16(stop) != 48 || int32(binary.LittleEndian.Uint32(stop[2:6])) != guard.ObjectID || readPoint(stop, 7, false) != guard.Position {
		t.Fatalf("bad guard stop packet: %x", stop)
	}
	if binary.LittleEndian.Uint16(visible) != 60 || int32(binary.LittleEndian.Uint32(visible[3:7])) != guard.ObjectID || binary.LittleEndian.Uint16(visible[14:16]) != guard.Direction {
		t.Fatalf("bad guard visible packet: %x", visible)
	}
	if binary.LittleEndian.Uint16(hp) != 78 || binary.LittleEndian.Uint32(hp[6:10]) != 9999 {
		t.Fatalf("bad guard HP packet: %x", hp)
	}
	syncPacket, err := syncGuardPacket(guard)
	if err != nil {
		t.Fatal(err)
	}
	syncPacket = decryptPacket(syncPacket)
	if binary.LittleEndian.Uint16(syncPacket) != 65 || binary.LittleEndian.Uint16(syncPacket[6:8]) != guard.Template || syncPacket[10] != 3 || syncPacket[11] != guard.Level || binary.LittleEndian.Uint32(syncPacket[12:16]) != 9999 {
		t.Fatalf("bad guard data packet: %x", syncPacket)
	}
}

func TestTeleportPacketsPreserveLegacyLayout(t *testing.T) {
	player := gameworld.Player{MapID: 143, RouteID: 1, Position: gameworld.Point{X: 100, Y: 200}, Altitude: 12}
	changed, err := changeMapPacket(player)
	if err != nil {
		t.Fatal(err)
	}
	changed = decryptPacket(changed)
	if len(changed) != 23 || binary.LittleEndian.Uint16(changed) != 41 || binary.LittleEndian.Uint32(changed[6:10]) != 143 || binary.LittleEndian.Uint32(changed[10:14]) != 1 || readPoint(changed, 14, false) != player.Position || binary.LittleEndian.Uint16(changed[18:20]) != 12 {
		t.Fatalf("bad change-map packet: %x", changed)
	}
	gameError, err := gameErrorPacket(4609)
	if err != nil {
		t.Fatal(err)
	}
	gameError = decryptPacket(gameError)
	if binary.LittleEndian.Uint16(gameError) != 9 || binary.LittleEndian.Uint32(gameError[2:6]) != 4609 {
		t.Fatalf("bad game-error packet: %x", gameError)
	}
}

func TestWorldPacketsPreserveLegacyCoordinates(t *testing.T) {
	player := gameworld.Player{ObjectID: 7, CharacterID: 7, Name: "Hero", MapID: 142, RouteID: 1, Position: gameworld.Point{X: 855, Y: 459}, Altitude: 10, Direction: 2048, Race: 1, Gender: 1, Hair: 2, HairColor: 3, Face: 4, Level: 1, CurrentHP: 80, MaxHP: 100, CurrentMP: 50, MaxMP: 100}
	syncPacket, err := syncCharacterPacket(player)
	if err != nil {
		t.Fatal(err)
	}
	syncPacket = decryptPacket(syncPacket)
	if binary.LittleEndian.Uint32(syncPacket[2:6]) != 7 || binary.LittleEndian.Uint32(syncPacket[6:10]) != 142 || readPoint(syncPacket, 62, false) != player.Position {
		t.Fatalf("bad sync packet: %x", syncPacket[:72])
	}
	visible, err := objectVisiblePacket(player)
	if err != nil {
		t.Fatal(err)
	}
	visible = decryptPacket(visible)
	if readPoint(visible, 8, false) != player.Position || visible[16] != 80 {
		t.Fatalf("bad visible packet: %x", visible)
	}
	movement := gameworld.Movement{Player: player, Kind: gameworld.MoveRan}
	move, err := movementPacket(movement)
	if err != nil {
		t.Fatal(err)
	}
	move = decryptPacket(move)
	if binary.LittleEndian.Uint16(move) != 47 || readPoint(move, 8, false) != player.Position {
		t.Fatalf("bad movement packet: %x", move)
	}
}
