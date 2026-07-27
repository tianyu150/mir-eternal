package gameworld

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type mapInstance struct {
	data     *MapData
	players  map[int32]*Player
	occupied map[Point]int32
}

type worldState struct {
	maps    map[int32]*mapInstance
	players map[int32]*Player
}

func newMapInstance(data *MapData) *mapInstance {
	instance := &mapInstance{data: data, players: make(map[int32]*Player), occupied: make(map[Point]int32)}
	for _, guard := range data.Guards {
		if guard.Blocking {
			instance.occupied[guard.Position] = guard.ObjectID
		}
	}
	return instance
}

type command interface{ apply(*World, *worldState) }

type World struct {
	catalog   *Catalog
	viewRange int32
	commands  chan command
	ready     chan struct{}
	done      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	running   atomic.Bool
}

func New(catalog *Catalog, viewRange int32) *World {
	if viewRange <= 0 {
		viewRange = 20
	}
	return &World{catalog: catalog, viewRange: viewRange, commands: make(chan command, 1024), ready: make(chan struct{}), done: make(chan struct{})}
}

func (w *World) Run(ctx context.Context) error {
	started := false
	w.startOnce.Do(func() { started = true })
	if !started {
		return errors.New("game world can only be run once")
	}
	w.running.Store(true)
	close(w.ready)
	defer func() {
		w.running.Store(false)
		w.stopOnce.Do(func() { close(w.done) })
	}()
	state := &worldState{maps: make(map[int32]*mapInstance), players: make(map[int32]*Player)}
	for {
		select {
		case <-ctx.Done():
			return nil
		case command := <-w.commands:
			command.apply(w, state)
		}
	}
}

func (w *World) WaitReady(ctx context.Context) error {
	select {
	case <-w.ready:
		return nil
	case <-w.done:
		return ErrNotRunning
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *World) submit(ctx context.Context, command command) error {
	if !w.running.Load() {
		return ErrNotRunning
	}
	select {
	case <-w.done:
		return ErrNotRunning
	default:
	}
	select {
	case w.commands <- command:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return ErrNotRunning
	}
}

type joinResponse struct {
	player Player
	err    error
}
type joinCommand struct {
	player   Player
	response chan joinResponse
}

func (c joinCommand) apply(w *World, state *worldState) {
	if _, exists := state.players[c.player.ObjectID]; exists {
		c.response <- joinResponse{err: ErrPlayerExists}
		return
	}
	data, err := w.catalog.Load(c.player.MapID)
	if err != nil {
		c.response <- joinResponse{err: err}
		return
	}
	instance := state.maps[c.player.MapID]
	if instance == nil {
		instance = newMapInstance(data)
		state.maps[c.player.MapID] = instance
	}
	if len(instance.players) >= data.Spec.LimitPlayers {
		c.response <- joinResponse{err: ErrMapFull}
		return
	}
	player := c.player
	player.RouteID = 1
	player.Active = false
	if !data.Terrain.CanPass(player.Position) {
		player.Position = instance.spawn(player.ObjectID)
	}
	player.Altitude = data.Terrain.Altitude(player.Position)
	state.players[player.ObjectID] = &player
	instance.players[player.ObjectID] = &player
	c.response <- joinResponse{player: player}
}

func (w *World) Join(ctx context.Context, player Player) (Player, error) {
	if err := player.Validate(); err != nil {
		return Player{}, err
	}
	response := make(chan joinResponse, 1)
	if err := w.submit(ctx, joinCommand{player: player, response: response}); err != nil {
		return Player{}, err
	}
	select {
	case result := <-response:
		return result.player, result.err
	case <-ctx.Done():
		return Player{}, ctx.Err()
	case <-w.done:
		return Player{}, ErrNotRunning
	}
}

type activationResponse struct {
	result Activation
	err    error
}
type activateCommand struct {
	objectID int32
	response chan activationResponse
}

func (c activateCommand) apply(w *World, state *worldState) {
	player := state.players[c.objectID]
	if player == nil {
		c.response <- activationResponse{err: ErrPlayerNotFound}
		return
	}
	if player.Active {
		instance := state.maps[player.MapID]
		c.response <- activationResponse{result: Activation{Player: *player, Visible: visiblePlayers(instance, *player, w.viewRange), Guards: visibleGuards(instance, player.Position, w.viewRange)}}
		return
	}
	instance := state.maps[player.MapID]
	if !instance.canOccupy(player.Position, player.ObjectID) {
		player.Position = instance.spawn(player.ObjectID)
	}
	player.Altitude = instance.data.Terrain.Altitude(player.Position)
	player.Active = true
	instance.occupied[player.Position] = player.ObjectID
	c.response <- activationResponse{result: Activation{Player: *player, Visible: visiblePlayers(instance, *player, w.viewRange), Guards: visibleGuards(instance, player.Position, w.viewRange)}}
}
func (w *World) Activate(ctx context.Context, objectID int32) (Activation, error) {
	response := make(chan activationResponse, 1)
	if err := w.submit(ctx, activateCommand{objectID, response}); err != nil {
		return Activation{}, err
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		return Activation{}, ctx.Err()
	case <-w.done:
		return Activation{}, ErrNotRunning
	}
}

type departureResponse struct {
	result Departure
	err    error
}
type leaveCommand struct {
	objectID int32
	response chan departureResponse
}

func (c leaveCommand) apply(w *World, state *worldState) {
	player := state.players[c.objectID]
	if player == nil {
		c.response <- departureResponse{err: ErrPlayerNotFound}
		return
	}
	instance := state.maps[player.MapID]
	observers := visibleIDs(instance, *player, w.viewRange)
	if player.Active {
		delete(instance.occupied, player.Position)
	}
	delete(instance.players, player.ObjectID)
	delete(state.players, player.ObjectID)
	c.response <- departureResponse{result: Departure{Player: *player, Observers: observers}}
}
func (w *World) Leave(ctx context.Context, objectID int32) (Departure, error) {
	response := make(chan departureResponse, 1)
	if err := w.submit(ctx, leaveCommand{objectID, response}); err != nil {
		return Departure{}, err
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		return Departure{}, ctx.Err()
	case <-w.done:
		return Departure{}, ErrNotRunning
	}
}

type moveResponse struct {
	result Movement
	err    error
}
type moveCommand struct {
	objectID int32
	target   Point
	run      bool
	response chan moveResponse
}

func (c moveCommand) apply(w *World, state *worldState) {
	player := state.players[c.objectID]
	if player == nil {
		c.response <- moveResponse{err: ErrPlayerNotFound}
		return
	}
	if !player.Active {
		c.response <- moveResponse{err: ErrPlayerNotActive}
		return
	}
	instance := state.maps[player.MapID]
	from := player.Position
	if c.target == from {
		c.response <- moveResponse{result: Movement{Player: *player, From: from, To: from, Kind: MoveStopped}}
		return
	}
	before := visibleMap(instance, *player, w.viewRange)
	beforeGuards := visibleGuardMap(instance, from, w.viewRange)
	direction := Direction(from, c.target)
	player.Direction = direction
	first := Step(from, direction, 1)
	kind := MoveWalked
	destination := first
	if !instance.canOccupy(first, player.ObjectID) {
		c.response <- moveResponse{result: Movement{Player: *player, From: from, To: from, Kind: MoveStopped}}
		return
	}
	if c.run {
		second := Step(from, direction, 2)
		if instance.canOccupy(second, player.ObjectID) {
			destination = second
			kind = MoveRan
		}
	}
	delete(instance.occupied, from)
	player.Position = destination
	player.Altitude = instance.data.Terrain.Altitude(destination)
	instance.occupied[destination] = player.ObjectID
	after := visibleMap(instance, *player, w.viewRange)
	afterGuards := visibleGuardMap(instance, destination, w.viewRange)
	result := Movement{Player: *player, From: from, To: destination, Kind: kind}
	for id, other := range after {
		if _, wasVisible := before[id]; wasVisible {
			result.Observers = append(result.Observers, id)
		} else {
			result.Entered = append(result.Entered, *other)
		}
	}
	for id, other := range before {
		if _, stillVisible := after[id]; !stillVisible {
			result.Left = append(result.Left, *other)
		}
	}
	for id, guard := range afterGuards {
		if _, wasVisible := beforeGuards[id]; !wasVisible {
			result.EnteredGuards = append(result.EnteredGuards, guard)
		}
	}
	for id, guard := range beforeGuards {
		if _, stillVisible := afterGuards[id]; !stillVisible {
			result.LeftGuards = append(result.LeftGuards, guard)
		}
	}
	c.response <- moveResponse{result: result}
}
func (w *World) Move(ctx context.Context, objectID int32, target Point, run bool) (Movement, error) {
	response := make(chan moveResponse, 1)
	if err := w.submit(ctx, moveCommand{objectID, target, run, response}); err != nil {
		return Movement{}, err
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		return Movement{}, ctx.Err()
	case <-w.done:
		return Movement{}, ErrNotRunning
	}
}

type rotationResponse struct {
	result Rotation
	err    error
}
type rotateCommand struct {
	objectID  int32
	direction uint16
	response  chan rotationResponse
}

func (c rotateCommand) apply(w *World, state *worldState) {
	if !validDirection(c.direction) {
		c.response <- rotationResponse{err: ErrInvalidDirection}
		return
	}
	player := state.players[c.objectID]
	if player == nil {
		c.response <- rotationResponse{err: ErrPlayerNotFound}
		return
	}
	if !player.Active {
		c.response <- rotationResponse{err: ErrPlayerNotActive}
		return
	}
	player.Direction = c.direction
	c.response <- rotationResponse{result: Rotation{Player: *player, Observers: visibleIDs(state.maps[player.MapID], *player, w.viewRange)}}
}
func (w *World) Rotate(ctx context.Context, objectID int32, direction uint16) (Rotation, error) {
	response := make(chan rotationResponse, 1)
	if err := w.submit(ctx, rotateCommand{objectID, direction, response}); err != nil {
		return Rotation{}, err
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		return Rotation{}, ctx.Err()
	case <-w.done:
		return Rotation{}, ErrNotRunning
	}
}

type transitionResponse struct {
	result Transition
	err    error
}
type teleportCommand struct {
	objectID int32
	gate     int32
	response chan transitionResponse
}

func (c teleportCommand) apply(w *World, state *worldState) {
	player := state.players[c.objectID]
	if player == nil {
		c.response <- transitionResponse{err: ErrPlayerNotFound}
		return
	}
	if !player.Active {
		c.response <- transitionResponse{err: ErrPlayerNotActive}
		return
	}
	gate, ok := w.catalog.Gate(player.MapID, c.gate)
	if !ok {
		c.response <- transitionResponse{err: ErrGateNotFound}
		return
	}
	if GridDistance(player.Position, gate.From) >= 8 {
		c.response <- transitionResponse{err: ErrGateTooFar}
		return
	}
	targetData, err := w.catalog.Load(gate.ToMapID)
	if err != nil {
		c.response <- transitionResponse{err: fmt.Errorf("load teleport destination: %w", err)}
		return
	}
	if player.Level < targetData.Spec.MinLevel {
		c.response <- transitionResponse{err: ErrLevelTooLow}
		return
	}
	source := state.maps[player.MapID]
	target := state.maps[gate.ToMapID]
	if target == nil {
		target = newMapInstance(targetData)
	}
	mapChanged := gate.ToMapID != player.MapID
	if mapChanged && len(target.players) >= targetData.Spec.LimitPlayers {
		c.response <- transitionResponse{err: ErrMapFull}
		return
	}
	destination, ok := target.nearestAvailable(gate.To, player.ObjectID, 3)
	if !ok {
		c.response <- transitionResponse{err: ErrDestination}
		return
	}
	fromMapID, from := player.MapID, player.Position
	oldObservers := visibleIDs(source, *player, w.viewRange)
	oldGuards := visibleGuards(source, player.Position, w.viewRange)
	delete(source.occupied, player.Position)
	if mapChanged {
		delete(source.players, player.ObjectID)
		state.maps[gate.ToMapID] = target
		target.players[player.ObjectID] = player
		player.MapID = gate.ToMapID
		player.RouteID = 1
		player.Position = destination
		player.Altitude = targetData.Terrain.Altitude(destination)
		player.Active = false
		c.response <- transitionResponse{result: Transition{Player: *player, Gate: gate, FromMapID: fromMapID, From: from, MapChanged: true, OldObservers: oldObservers, OldGuards: oldGuards}}
		return
	}
	player.Position = destination
	player.Altitude = targetData.Terrain.Altitude(destination)
	target.occupied[destination] = player.ObjectID
	c.response <- transitionResponse{result: Transition{Player: *player, Gate: gate, FromMapID: fromMapID, From: from, OldObservers: oldObservers, OldGuards: oldGuards, NewVisible: visiblePlayers(target, *player, w.viewRange), NewGuards: visibleGuards(target, player.Position, w.viewRange)}}
}

func (w *World) Teleport(ctx context.Context, objectID, gate int32) (Transition, error) {
	response := make(chan transitionResponse, 1)
	if err := w.submit(ctx, teleportCommand{objectID: objectID, gate: gate, response: response}); err != nil {
		return Transition{}, err
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		return Transition{}, ctx.Err()
	case <-w.done:
		return Transition{}, ErrNotRunning
	}
}

type statsResponse struct{ maps, players, active, guards int }
type statsCommand struct{ response chan statsResponse }

func (c statsCommand) apply(_ *World, state *worldState) {
	result := statsResponse{maps: len(state.maps), players: len(state.players)}
	for _, player := range state.players {
		if player.Active {
			result.active++
		}
	}
	for _, instance := range state.maps {
		result.guards += len(instance.data.Guards)
	}
	c.response <- result
}
func (w *World) Stats(ctx context.Context) (maps, players, active, guards int) {
	response := make(chan statsResponse, 1)
	if err := w.submit(ctx, statsCommand{response}); err != nil {
		return 0, 0, 0, 0
	}
	select {
	case result := <-response:
		return result.maps, result.players, result.active, result.guards
	case <-ctx.Done():
		return 0, 0, 0, 0
	case <-w.done:
		return 0, 0, 0, 0
	}
}

func (instance *mapInstance) canOccupy(point Point, objectID int32) bool {
	if !instance.data.Terrain.CanPass(point) {
		return false
	}
	occupant, occupied := instance.occupied[point]
	return !occupied || occupant == objectID
}
func (instance *mapInstance) nearestAvailable(center Point, objectID, radius int32) (Point, bool) {
	if instance.canOccupy(center, objectID) {
		return center, true
	}
	for distance := int32(1); distance <= radius; distance++ {
		for x := center.X - distance; x <= center.X+distance; x++ {
			for _, y := range []int32{center.Y - distance, center.Y + distance} {
				candidate := Point{X: x, Y: y}
				if instance.canOccupy(candidate, objectID) {
					return candidate, true
				}
			}
		}
		for y := center.Y - distance + 1; y < center.Y+distance; y++ {
			for _, x := range []int32{center.X - distance, center.X + distance} {
				candidate := Point{X: x, Y: y}
				if instance.canOccupy(candidate, objectID) {
					return candidate, true
				}
			}
		}
	}
	return Point{}, false
}

func (instance *mapInstance) spawn(seed int32) Point {
	points := instance.data.Resurrection
	if len(points) > 0 {
		start := int(seed)
		if start < 0 {
			start = -start
		}
		for offset := 0; offset < len(points); offset++ {
			point := points[(start+offset)%len(points)]
			if instance.canOccupy(point, seed) {
				return point
			}
		}
	}
	for x := instance.data.Terrain.Start.X; x < instance.data.Terrain.End.X; x++ {
		for y := instance.data.Terrain.Start.Y; y < instance.data.Terrain.End.Y; y++ {
			point := Point{X: x, Y: y}
			if instance.canOccupy(point, seed) {
				return point
			}
		}
	}
	return instance.data.Terrain.Start
}
func visibleMap(instance *mapInstance, player Player, distance int32) map[int32]*Player {
	result := make(map[int32]*Player)
	for id, other := range instance.players {
		if id != player.ObjectID && other.Active && GridDistance(player.Position, other.Position) <= distance {
			result[id] = other
		}
	}
	return result
}
func visiblePlayers(instance *mapInstance, player Player, distance int32) []Player {
	visible := visibleMap(instance, player, distance)
	result := make([]Player, 0, len(visible))
	for _, other := range visible {
		result = append(result, *other)
	}
	return result
}
func visibleIDs(instance *mapInstance, player Player, distance int32) []int32 {
	visible := visibleMap(instance, player, distance)
	result := make([]int32, 0, len(visible))
	for id := range visible {
		result = append(result, id)
	}
	return result
}

func visibleGuardMap(instance *mapInstance, position Point, distance int32) map[int32]Guard {
	result := make(map[int32]Guard)
	for _, guard := range instance.data.Guards {
		if GridDistance(position, guard.Position) <= distance {
			result[guard.ObjectID] = guard
		}
	}
	return result
}

func visibleGuards(instance *mapInstance, position Point, distance int32) []Guard {
	visible := visibleGuardMap(instance, position, distance)
	result := make([]Guard, 0, len(visible))
	for _, guard := range visible {
		result = append(result, guard)
	}
	return result
}

func (p Player) Validate() error {
	if p.ObjectID <= 0 || p.CharacterID <= 0 || p.MapID <= 0 || p.Name == "" {
		return fmt.Errorf("invalid world player")
	}
	return nil
}

type playerResponse struct {
	player Player
	err    error
}
type playerCommand struct {
	objectID int32
	response chan playerResponse
}

func (c playerCommand) apply(_ *World, state *worldState) {
	player := state.players[c.objectID]
	if player == nil {
		c.response <- playerResponse{err: ErrPlayerNotFound}
		return
	}
	c.response <- playerResponse{player: *player}
}
func (w *World) Player(ctx context.Context, objectID int32) (Player, error) {
	response := make(chan playerResponse, 1)
	if err := w.submit(ctx, playerCommand{objectID, response}); err != nil {
		return Player{}, err
	}
	select {
	case result := <-response:
		return result.player, result.err
	case <-ctx.Done():
		return Player{}, ctx.Err()
	case <-w.done:
		return Player{}, ErrNotRunning
	}
}

type guardResponse struct {
	guard Guard
	err   error
}
type guardCommand struct {
	playerID int32
	guardID  int32
	response chan guardResponse
}

func (c guardCommand) apply(w *World, state *worldState) {
	player := state.players[c.playerID]
	if player == nil || !player.Active {
		c.response <- guardResponse{err: ErrPlayerNotActive}
		return
	}
	guard, ok := w.catalog.Guard(c.guardID)
	if !ok || guard.MapID != player.MapID || GridDistance(player.Position, guard.Position) > w.viewRange {
		c.response <- guardResponse{err: ErrPlayerNotFound}
		return
	}
	c.response <- guardResponse{guard: guard}
}

func (w *World) GuardForPlayer(ctx context.Context, playerID, guardID int32) (Guard, error) {
	response := make(chan guardResponse, 1)
	if err := w.submit(ctx, guardCommand{playerID: playerID, guardID: guardID, response: response}); err != nil {
		return Guard{}, err
	}
	select {
	case result := <-response:
		return result.guard, result.err
	case <-ctx.Done():
		return Guard{}, ctx.Err()
	case <-w.done:
		return Guard{}, ErrNotRunning
	}
}
