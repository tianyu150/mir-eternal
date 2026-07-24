package gameprotocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestReadLoginPacket(t *testing.T) {
	wire := make([]byte, 162)
	binary.LittleEndian.PutUint16(wire, 1001)
	copy(wire[72:110], "ULS21-ticket")
	copy(wire[136:153], "00:11:22:33:44")
	frame, err := NewReader(bytes.NewReader(wire), 1024).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.ID() != 1001 || string(bytes.TrimRight(frame.Data[72:110], "\x00")) != "ULS21-ticket" {
		t.Fatalf("unexpected frame: %+v", frame)
	}
}

func TestReadEncryptedFixedPacket(t *testing.T) {
	descriptor := ClientPackets[18]
	decoded := make([]byte, descriptor.Length)
	binary.LittleEndian.PutUint16(decoded, descriptor.ID)
	binary.LittleEndian.PutUint32(decoded[2:6], 0x12345678)
	wire, err := EncodeFixed(descriptor, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(wire[4:], decoded[4:]) {
		t.Fatal("payload was not encrypted")
	}
	frame, err := NewReader(bytes.NewReader(wire), 1024).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Data, decoded) {
		t.Fatalf("decoded %x, want %x", frame.Data, decoded)
	}
}

func TestReadVariablePacket(t *testing.T) {
	descriptor := ClientPackets[131]
	decoded := []byte{byte(descriptor.ID), byte(descriptor.ID >> 8), 8, 0, 1, 2, 3, 4}
	wire := append([]byte(nil), decoded...)
	xor(wire)
	frame, err := NewReader(bytes.NewReader(wire), 1024).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Data, decoded) {
		t.Fatalf("decoded %x, want %x", frame.Data, decoded)
	}
}

func TestRejectLengthBomb(t *testing.T) {
	wire := []byte{131, 0, 0xff, 0xff}
	_, err := NewReader(bytes.NewReader(wire), 1024).ReadFrame()
	if !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("expected invalid length, got %v", err)
	}
}

func TestBuildPacket(t *testing.T) {
	wire, err := Build(1010, func(data []byte) { binary.LittleEndian.PutUint32(data[2:6], 1234) })
	if err != nil {
		t.Fatal(err)
	}
	xor(wire)
	if binary.LittleEndian.Uint16(wire) != 1010 || binary.LittleEndian.Uint32(wire[2:6]) != 1234 {
		t.Fatalf("unexpected packet %x", wire)
	}
	variable, err := BuildVariable(1002, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(variable[2:4]) != 7 {
		t.Fatalf("unexpected variable packet %x", variable)
	}
}
