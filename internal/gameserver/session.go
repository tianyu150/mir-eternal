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
	"github.com/tianyu150/mir-eternal/internal/gameworld"
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
	objectID     int32
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
	if s.stage == loadingScene && (frame.ID() == 12 || frame.ID() == 642) {
		return s.handleEnterScene(ctx)
	}
	if s.stage == playingScene {
		switch frame.ID() {
		case 10:
			return s.handleChangeCharacter(ctx)
		case 14:
			return s.handlePositionSync()
		case 16:
			return s.handleRotate(ctx, frame)
		case 17:
			return s.handleMove(ctx, frame, true)
		case 18:
			return s.handleMove(ctx, frame, false)
		case 19:
			return s.handleObjectData(ctx, frame)
		case 22:
			return s.handleTeleport(ctx, frame)
		case 271:
			return nil
		}
	}
	// The complete catalog can be framed safely. Remaining domain handlers are
	// migrated incrementally and are counted instead of returning false success.
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
	if s.objectID != 0 {
		return errors.New("character is already in world")
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
	currentHP := character.CurrentHP
	if currentHP <= 0 {
		currentHP = 100
	}
	currentMP := character.CurrentMP
	if currentMP < 0 {
		currentMP = 0
	}
	player, err := s.server.world.Join(ctx, gameworld.Player{ObjectID: character.ID, CharacterID: character.ID, Account: s.account, Name: character.Name, MapID: character.MapID, Position: gameworld.Point{X: character.PositionX, Y: character.PositionY}, Direction: character.Direction, Race: character.Race, Gender: character.Gender, Hair: character.Hair, HairColor: character.HairColor, Face: character.Face, Level: character.Level, CurrentHP: currentHP, MaxHP: 100, CurrentMP: currentMP, MaxMP: 100})
	if err != nil {
		packet, _ := loginErrorPacket(284, 0, 0)
		_ = s.writePackets(packet)
		return nil
	}
	if !s.server.attachWorld(s, player.ObjectID) {
		_, _ = s.server.world.Leave(ctx, player.ObjectID)
		return errors.New("world object is already connected")
	}
	s.characterID, s.objectID = id, player.ObjectID
	answer, err := integerPacket(1003, id)
	if err != nil {
		return err
	}
	syncCharacter, err := syncCharacterPacket(player)
	if err != nil {
		return err
	}
	endSync, err := endSyncPacket(id)
	if err != nil {
		return err
	}
	if err := s.writePackets(answer, syncCharacter, endSync); err != nil {
		return err
	}
	s.stage = loadingScene
	return nil
}
func (s *session) handleEnterScene(ctx context.Context) error {
	activation, err := s.server.world.Activate(ctx, s.objectID)
	if err != nil {
		return err
	}
	stop, err := stopPacket(activation.Player)
	if err != nil {
		return err
	}
	enter, err := enterScenePacket(activation.Player)
	if err != nil {
		return err
	}
	selfVisible, err := objectVisiblePacket(activation.Player)
	if err != nil {
		return err
	}
	hp, err := objectHPPacket(activation.Player)
	if err != nil {
		return err
	}
	mp, err := objectMPPacket(activation.Player)
	if err != nil {
		return err
	}
	packets := [][]byte{stop, enter, selfVisible, hp, mp}
	observerIDs := make([]int32, 0, len(activation.Visible))
	for _, visible := range activation.Visible {
		visiblePackets, err := visibleObjectPackets(visible)
		if err != nil {
			return err
		}
		packets = append(packets, visiblePackets...)
		observerIDs = append(observerIDs, visible.ObjectID)
	}
	for _, guard := range activation.Guards {
		guardPackets, err := guardVisiblePackets(guard)
		if err != nil {
			return err
		}
		packets = append(packets, guardPackets...)
	}
	if err := s.writePackets(packets...); err != nil {
		return err
	}
	appearance, err := visibleObjectPackets(activation.Player)
	if err != nil {
		return err
	}
	s.server.sendToObjects(observerIDs, appearance...)
	s.stage = playingScene
	return nil
}

func (s *session) handlePositionSync() error {
	// Packet 14 is client telemetry. Never trust it as authoritative state.
	// Return the current world position through the standard stop packet.
	if s.objectID == 0 {
		return gameworld.ErrPlayerNotFound
	}
	player, err := s.server.world.Player(context.Background(), s.objectID)
	if err != nil {
		return err
	}
	packet, err := stopPacket(player)
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}

func (s *session) handleRotate(ctx context.Context, frame gameprotocol.Frame) error {
	if len(frame.Data) != 8 {
		return errors.New("invalid rotation packet")
	}
	direction := uint16(int16(binary.LittleEndian.Uint16(frame.Data[2:4])))
	rotation, err := s.server.world.Rotate(ctx, s.objectID, direction)
	if err != nil {
		return err
	}
	packet, err := rotationPacket(rotation)
	if err != nil {
		return err
	}
	if err := s.writePackets(packet); err != nil {
		return err
	}
	s.server.sendToObjects(rotation.Observers, packet)
	return nil
}

func (s *session) handleMove(ctx context.Context, frame gameprotocol.Frame, run bool) error {
	if len(frame.Data) != 6 {
		return errors.New("invalid movement packet")
	}
	target := readPoint(frame.Data, 2, run)
	movement, err := s.server.world.Move(ctx, s.objectID, target, run)
	if err != nil {
		return err
	}
	packet, err := movementPacket(movement)
	if err != nil {
		return err
	}
	if err := s.writePackets(packet); err != nil {
		return err
	}
	if movement.Kind == gameworld.MoveStopped {
		return nil
	}
	s.server.sendToObjects(movement.Observers, packet)
	for _, entered := range movement.Entered {
		packets, err := visibleObjectPackets(entered)
		if err != nil {
			return err
		}
		if err := s.writePackets(packets...); err != nil {
			return err
		}
		selfPackets, err := visibleObjectPackets(movement.Player)
		if err != nil {
			return err
		}
		s.server.sendToObjects([]int32{entered.ObjectID}, selfPackets...)
	}
	for _, left := range movement.Left {
		outOther, err := objectOutPacket(left.ObjectID)
		if err != nil {
			return err
		}
		if err := s.writePackets(outOther); err != nil {
			return err
		}
		outSelf, err := objectOutPacket(movement.Player.ObjectID)
		if err != nil {
			return err
		}
		s.server.sendToObjects([]int32{left.ObjectID}, outSelf)
	}
	for _, guard := range movement.EnteredGuards {
		packets, err := guardVisiblePackets(guard)
		if err != nil {
			return err
		}
		if err := s.writePackets(packets...); err != nil {
			return err
		}
	}
	for _, guard := range movement.LeftGuards {
		out, err := objectOutPacket(guard.ObjectID)
		if err != nil {
			return err
		}
		if err := s.writePackets(out); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) handleObjectData(ctx context.Context, frame gameprotocol.Frame) error {
	if len(frame.Data) != 10 {
		return errors.New("invalid object data request")
	}
	objectID := int32(binary.LittleEndian.Uint32(frame.Data[2:6]))
	guard, err := s.server.world.GuardForPlayer(ctx, s.objectID, objectID)
	if err != nil {
		packet, packetErr := socialErrorPacket(6732)
		if packetErr != nil {
			return packetErr
		}
		return s.writePackets(packet)
	}
	packet, err := syncGuardPacket(guard)
	if err != nil {
		return err
	}
	return s.writePackets(packet)
}

func (s *session) handleTeleport(ctx context.Context, frame gameprotocol.Frame) error {
	if len(frame.Data) != 6 {
		return errors.New("invalid teleport gate packet")
	}
	gate := int32(binary.LittleEndian.Uint32(frame.Data[2:6]))
	transition, err := s.server.world.Teleport(ctx, s.objectID, gate)
	if err != nil {
		code := int32(775)
		switch {
		case errors.Is(err, gameworld.ErrGateTooFar):
			code = 4609
		case errors.Is(err, gameworld.ErrLevelTooLow):
			code = 4624
		case errors.Is(err, gameworld.ErrDestination), errors.Is(err, gameworld.ErrMapFull):
			code = 769
		case !errors.Is(err, gameworld.ErrGateNotFound):
			return err
		}
		packet, packetErr := gameErrorPacket(code)
		if packetErr != nil {
			return packetErr
		}
		return s.writePackets(packet)
	}
	if err := s.server.store.SaveWorldState(ctx, s.account, s.characterID, gamestore.WorldState{MapID: transition.Player.MapID, PositionX: transition.Player.Position.X, PositionY: transition.Player.Position.Y, Direction: transition.Player.Direction, CurrentHP: transition.Player.CurrentHP, CurrentMP: transition.Player.CurrentMP}); err != nil {
		return err
	}
	leave, err := leaveScenePacket()
	if err != nil {
		return err
	}
	outSelf, err := objectOutPacket(transition.Player.ObjectID)
	if err != nil {
		return err
	}
	s.server.sendToObjects(transition.OldObservers, outSelf)
	if transition.MapChanged {
		changed, err := changeMapPacket(transition.Player)
		if err != nil {
			return err
		}
		if err := s.writePackets(leave, changed); err != nil {
			return err
		}
		s.stage = loadingScene
		return nil
	}
	packets := [][]byte{leave}
	for _, observerID := range transition.OldObservers {
		out, err := objectOutPacket(observerID)
		if err != nil {
			return err
		}
		packets = append(packets, out)
	}
	for _, guard := range transition.OldGuards {
		out, err := objectOutPacket(guard.ObjectID)
		if err != nil {
			return err
		}
		packets = append(packets, out)
	}
	stop, err := stopPacket(transition.Player)
	if err != nil {
		return err
	}
	enter, err := enterScenePacket(transition.Player)
	if err != nil {
		return err
	}
	packets = append(packets, stop, enter)
	observerIDs := make([]int32, 0, len(transition.NewVisible))
	for _, visible := range transition.NewVisible {
		visiblePackets, err := visibleObjectPackets(visible)
		if err != nil {
			return err
		}
		packets = append(packets, visiblePackets...)
		observerIDs = append(observerIDs, visible.ObjectID)
	}
	for _, guard := range transition.NewGuards {
		guardPackets, err := guardVisiblePackets(guard)
		if err != nil {
			return err
		}
		packets = append(packets, guardPackets...)
	}
	if err := s.writePackets(packets...); err != nil {
		return err
	}
	selfPackets, err := visibleObjectPackets(transition.Player)
	if err != nil {
		return err
	}
	s.server.sendToObjects(observerIDs, selfPackets...)
	return nil
}

func (s *session) handleChangeCharacter(ctx context.Context) error {
	s.leaveWorld()
	changed, err := gameprotocol.Build(1009, nil)
	if err != nil {
		return err
	}
	characters, err := s.server.store.ListCharacters(ctx, s.account)
	if err != nil {
		return err
	}
	list, err := characterListPacket(characters)
	if err != nil {
		return err
	}
	if err := s.writePackets(changed, list); err != nil {
		return err
	}
	s.stage = selectingCharacter
	return nil
}

func (s *session) leaveWorld() {
	if s.objectID == 0 || s.server.world == nil {
		return
	}
	objectID := s.objectID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	departure, err := s.server.world.Leave(ctx, objectID)
	if err == nil {
		_ = s.server.store.SaveWorldState(ctx, s.account, s.characterID, gamestore.WorldState{MapID: departure.Player.MapID, PositionX: departure.Player.Position.X, PositionY: departure.Player.Position.Y, Direction: departure.Player.Direction, CurrentHP: departure.Player.CurrentHP, CurrentMP: departure.Player.CurrentMP})
		out, packetErr := objectOutPacket(objectID)
		if packetErr == nil {
			s.server.sendToObjects(departure.Observers, out)
		}
	}
	s.server.mu.Lock()
	if s.server.worldPeers[objectID] == s {
		delete(s.server.worldPeers, objectID)
	}
	s.server.mu.Unlock()
	s.objectID, s.characterID = 0, 0
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
