// Package gameprotocol implements Mir Eternal's little-endian TCP framing and
// XOR transformation. Packet field handlers can be migrated independently of
// framing because the complete C# packet catalog is generated into this package.
package gameprotocol

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const EncryptionKey byte = 129

type Direction uint8

const (
	Client Direction = iota
	Server
)

type Descriptor struct {
	ID         uint16
	Length     uint32 // Zero means a length-prefixed packet.
	Name       string
	UseIntSize bool
	Encrypted  bool
}

type Frame struct {
	Descriptor Descriptor
	// Data contains the complete decoded packet, including ID and length fields.
	Data []byte
}

func (f Frame) ID() uint16 { return f.Descriptor.ID }

var (
	ErrUnknownPacket = errors.New("unknown packet")
	ErrInvalidLength = errors.New("invalid packet length")
)

type Reader struct {
	reader *bufio.Reader
	max    uint32
}

func NewReader(reader io.Reader, maxPacketBytes uint32) *Reader {
	if maxPacketBytes == 0 {
		maxPacketBytes = 1 << 20
	}
	return &Reader{reader: bufio.NewReader(reader), max: maxPacketBytes}
}

func (r *Reader) ReadFrame() (Frame, error) {
	prefix, err := r.reader.Peek(2)
	if err != nil {
		return Frame{}, err
	}
	id := binary.LittleEndian.Uint16(prefix)
	descriptor, ok := ClientPackets[id]
	if !ok {
		return Frame{}, fmt.Errorf("%w: client packet 0x%04x", ErrUnknownPacket, id)
	}
	length := descriptor.Length
	headerLength := uint32(2)
	if length == 0 {
		headerLength = 4
		if descriptor.UseIntSize {
			headerLength = 6
		}
		header, err := r.reader.Peek(int(headerLength))
		if err != nil {
			return Frame{}, err
		}
		copyHeader := append([]byte(nil), header...)
		if descriptor.Encrypted {
			xor(copyHeader)
		}
		if descriptor.UseIntSize {
			length = binary.LittleEndian.Uint32(copyHeader[2:6])
		} else {
			length = uint32(binary.LittleEndian.Uint16(copyHeader[2:4]))
		}
	}
	if length < headerLength || length > r.max {
		return Frame{}, fmt.Errorf("%w: packet 0x%04x has length %d", ErrInvalidLength, id, length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r.reader, data); err != nil {
		return Frame{}, err
	}
	if descriptor.Encrypted {
		xor(data)
	}
	return Frame{Descriptor: descriptor, Data: data}, nil
}

// Build creates an encoded server packet. fill writes fields into the decoded
// complete frame at their protocol offsets.
func Build(id uint16, fill func([]byte)) ([]byte, error) {
	descriptor, ok := ServerPackets[id]
	if !ok {
		return nil, fmt.Errorf("%w: server packet 0x%04x", ErrUnknownPacket, id)
	}
	if descriptor.Length == 0 {
		return nil, errors.New("use BuildVariable for a variable-length packet")
	}
	data := make([]byte, descriptor.Length)
	binary.LittleEndian.PutUint16(data, id)
	if fill != nil {
		fill(data)
	}
	if descriptor.Encrypted {
		xor(data)
	}
	return data, nil
}

func BuildVariable(id uint16, payload []byte) ([]byte, error) {
	descriptor, ok := ServerPackets[id]
	if !ok {
		return nil, fmt.Errorf("%w: server packet 0x%04x", ErrUnknownPacket, id)
	}
	if descriptor.Length != 0 {
		return nil, errors.New("use Build for a fixed-length packet")
	}
	headerLength := 4
	if descriptor.UseIntSize {
		headerLength = 6
	}
	length := headerLength + len(payload)
	data := make([]byte, length)
	binary.LittleEndian.PutUint16(data, id)
	if descriptor.UseIntSize {
		binary.LittleEndian.PutUint32(data[2:6], uint32(length))
	} else {
		if length > int(^uint16(0)) {
			return nil, ErrInvalidLength
		}
		binary.LittleEndian.PutUint16(data[2:4], uint16(length))
	}
	copy(data[headerLength:], payload)
	if descriptor.Encrypted {
		xor(data)
	}
	return data, nil
}

// EncodeFixed is useful for tests and packet forwarding in migration adapters.
func EncodeFixed(descriptor Descriptor, decoded []byte) ([]byte, error) {
	if uint32(len(decoded)) != descriptor.Length || len(decoded) < 2 || binary.LittleEndian.Uint16(decoded[:2]) != descriptor.ID {
		return nil, ErrInvalidLength
	}
	out := append([]byte(nil), decoded...)
	if descriptor.Encrypted {
		xor(out)
	}
	return out, nil
}

func xor(data []byte) {
	for index := 4; index < len(data); index++ {
		data[index] ^= EncryptionKey
	}
}
