package gameserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tianyu150/mir-eternal/internal/gameprotocol"
	"github.com/tianyu150/mir-eternal/internal/gamestore"
)

type stage uint8

const (
	startingSession stage = iota
	selectingCharacter
	loadingScene
	playingScene
)

type session struct {
	server       *Server
	conn         net.Conn
	reader       *gameprotocol.Reader
	writeMu      sync.Mutex
	closeOnce    sync.Once
	stage        stage
	account, mac string
	characterID  int32
}

func newSession(server *Server, conn net.Conn) *session {
	return &session{server: server, conn: conn, reader: gameprotocol.NewReader(conn, server.config.MaxPacketBytes), stage: startingSession}
}
func (s *session) run(ctx context.Context) {
	remote := s.conn.RemoteAddr().String()
	s.server.logger.Debug("game client connected", "remote", remote)
	defer s.server.logger.Debug("game client disconnected", "remote", remote, "account", s.account)
	_ = s.conn.SetReadDeadline(time.Now().Add(s.server.config.LoginTimeout))
	for {
		frame, err := s.reader.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.server.logger.Debug("game client read stopped", "remote", remote, "error", err)
			}
			return
		}
		s.server.stats.bytesReceived.Add(int64(len(frame.Data)))
		s.server.stats.packetsReceived.Add(1)
		_ = s.conn.SetReadDeadline(time.Now().Add(s.server.config.IdleTimeout))
		if err := s.handle(ctx, frame); err != nil {
			s.server.logger.Debug("game packet rejected", "remote", remote, "packet", frame.Descriptor.Name, "error", err)
			s.server.stats.rejected.Add(1)
			return
		}
	}
}
func (s *session) handle(ctx context.Context, frame gameprotocol.Frame) error {
	if frame.ID() == 1001 {
		return s.handleLogin(ctx, frame)
	}
	if s.account == "" {
		return errors.New("account is not authenticated")
	}
	if s.stage == selectingCharacter {
		switch frame.ID() {
		case 1002:
			return s.handleCreate(ctx, frame)
		case 1003:
			return s.handleFreeze(ctx, frame)
		case 1004:
			return s.handleDelete(ctx, frame)
		case 1005:
			return s.handleRestore(ctx, frame)
		case 1006:
			return s.handleEnter(ctx, frame)
		case 1007:
			return s.handlePing(frame)
		}
	}
	if frame.ID() == 1007 {
		return s.handlePing(frame)
	}
	if s.stage == loadingScene && (frame.ID() == 642 || frame.ID() == 271) {
		s.stage = playingScene
		return nil
	}
	// Framing for the complete catalog is available. World/map handlers are
	// intentionally migrated behind this dispatch point in subsequent slices.
	s.server.stats.unhandledPackets.Add(1)
	return nil
}
func (s *session) handleLogin(ctx context.Context, frame gameprotocol.Frame) error {
	if s.stage != startingSession || len(frame.Data) != 162 {
		return errors.New("login packet in invalid stage")
	}
	value := nullTerminated(frame.Data[72:110])
	mac := nullTerminated(frame.Data[136:153])
	if value == "" {
		return errors.New("empty login ticket")
	}
	login, err := s.server.tickets.Consume(value)
	if err != nil {
		return err
	}
	account, err := s.server.store.EnsureAccount(ctx, login.Account)
	if err != nil {
		return err
	}
	if account.BanUntil.After(time.Now()) {
		packet, _ := loginErrorPacket(285, protocolTime(account.BanUntil), 0)
		_ = s.writePackets(packet)
		return errors.New("account is banned")
	}
	if !s.server.claimAccount(s, account.Name) {
		return errors.New("account is already online")
	}
	s.account = account.Name
	s.mac = mac
	characters, err := s.server.store.ListCharacters(ctx, s.account)
	if err != nil {
		return err
	}
	loginPacket, err := loginSuccessPacket()
	if err != nil {
		return err
	}
	status, err := serviceStatusPacket()
	if err != nil {
		return err
	}
	tuning, err := tuningPacket()
	if err != nil {
		return err
	}
	extension, err := extensionPacket()
	if err != nil {
		return err
	}
	list, err := characterListPacket(characters)
	if err != nil {
		return err
	}
	if err := s.writePackets(loginPacket, status, tuning, extension, list); err != nil {
		return err
	}
	s.stage = selectingCharacter
	return nil
}
func (s *session) handleCreate(ctx context.Context, frame gameprotocol.Frame) error {
	if len(frame.Data) != 40 {
		return errors.New("invalid create character packet")
	}
	request := gamestore.CreateCharacter{Name: nullTerminated(frame.Data[2:34]), Gender: frame.Data[34], Race: frame.Data[35], Hair: frame.Data[36], HairColor: frame.Data[37], Face: frame.Data[38]}
	character, err := s.server.store.CreateCharacter(ctx, s.account, request)
	if err != nil {
		code := uint32(258)
		if errors.Is(err, gamestore.ErrCharacterExists) {
			code = 272
		} else if errors.Is(err, gamestore.ErrActiveSlotsFull) {
			code = 267
		} else if errors.Is(err, gamestore.ErrInvalidCharacter) {
			code = 270
		}
		packet, _ := loginErrorPacket(code, 0, 0)
		_ = s.writePackets(packet)
		return nil
	}
	packet, err := characterCreatedPacket(character)
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}
func (s *session) handleFreeze(ctx context.Context, frame gameprotocol.Frame) error {
	id, err := packetCharacterID(frame)
	if err != nil {
		return err
	}
	if _, err := s.server.store.FreezeCharacter(ctx, s.account, id); err != nil {
		packet, _ := loginErrorPacket(277, 0, 0)
		return s.writePackets(packet)
	}
	packet, err := integerPacket(1006, id)
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}
func (s *session) handleDelete(ctx context.Context, frame gameprotocol.Frame) error {
	id, err := packetCharacterID(frame)
	if err != nil {
		return err
	}
	if _, err := s.server.store.DeleteCharacter(ctx, s.account, id); err != nil {
		code := uint32(277)
		if errors.Is(err, gamestore.ErrDeleteRestricted) {
			code = 291
		}
		packet, _ := loginErrorPacket(code, 0, 0)
		return s.writePackets(packet)
	}
	packet, err := integerPacket(1008, id)
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}
func (s *session) handleRestore(ctx context.Context, frame gameprotocol.Frame) error {
	id, err := packetCharacterID(frame)
	if err != nil {
		return err
	}
	if _, err := s.server.store.RestoreCharacter(ctx, s.account, id); err != nil {
		packet, _ := loginErrorPacket(277, 0, 0)
		return s.writePackets(packet)
	}
	packet, err := integerPacket(1007, id)
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}
func (s *session) handleEnter(ctx context.Context, frame gameprotocol.Frame) error {
	id, err := packetCharacterID(frame)
	if err != nil {
		return err
	}
	character, err := s.server.store.ActiveCharacter(ctx, s.account, id)
	if err != nil {
		packet, _ := loginErrorPacket(284, 0, 0)
		return s.writePackets(packet)
	}
	if character.BanUntil.After(time.Now()) {
		packet, _ := loginErrorPacket(285, protocolTime(character.BanUntil), 0)
		return s.writePackets(packet)
	}
	packet, err := integerPacket(1003, id)
	if err != nil {
		return err
	}
	if err := s.writePackets(packet); err != nil {
		return err
	}
	s.characterID = id
	s.stage = loadingScene
	return nil
}
func (s *session) handlePing(frame gameprotocol.Frame) error {
	if len(frame.Data) != 6 {
		return errors.New("invalid ping packet")
	}
	packet, err := integerPacket(1010, int32(binary.LittleEndian.Uint32(frame.Data[2:6])))
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}
func packetCharacterID(frame gameprotocol.Frame) (int32, error) {
	if len(frame.Data) != 6 {
		return 0, errors.New("invalid character packet")
	}
	return int32(binary.LittleEndian.Uint32(frame.Data[2:6])), nil
}
func nullTerminated(data []byte) string {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		data = data[:index]
	}
	return strings.TrimSpace(string(data))
}
func (s *session) writePackets(packets ...[]byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if len(packets) == 0 {
		return nil
	}
	total := 0
	for _, packet := range packets {
		total += len(packet)
	}
	buffer := make([]byte, 0, total)
	for _, packet := range packets {
		buffer = append(buffer, packet...)
	}
	for len(buffer) > 0 {
		n, err := s.conn.Write(buffer)
		if err != nil {
			return fmt.Errorf("write game packets: %w", err)
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		s.server.stats.bytesSent.Add(int64(n))
		buffer = buffer[n:]
	}
	s.server.stats.packetsSent.Add(int64(len(packets)))
	return nil
}
func (s *session) close() { s.closeOnce.Do(func() { _ = s.conn.Close() }) }
