package gameworld

import (
	"context"
	"errors"
	"testing"
)

func startTestWorld(t *testing.T, viewRange int32) (*World, context.CancelFunc) {
	t.Helper()
	world := New(makeTestCatalog(t), viewRange)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = world.Run(ctx) }()
	if err := world.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	return world, cancel
}

func TestWorldPlayerLifecycleAndMovement(t *testing.T) {
	world, cancel := startTestWorld(t, 3)
	defer cancel()
	ctx := context.Background()
	first := Player{ObjectID: 1, CharacterID: 1, Account: "a", Name: "One", MapID: 142, Position: Point{2, 2}, CurrentHP: 100, MaxHP: 100}
	second := Player{ObjectID: 2, CharacterID: 2, Account: "b", Name: "Two", MapID: 142, Position: Point{4, 2}, CurrentHP: 100, MaxHP: 100}
	if _, err := world.Join(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := world.Join(ctx, second); err != nil {
		t.Fatal(err)
	}
	activation, err := world.Activate(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(activation.Visible) != 0 {
		t.Fatalf("inactive player was visible: %+v", activation.Visible)
	}
	activation, err = world.Activate(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(activation.Visible) != 1 || activation.Visible[0].ObjectID != 1 {
		t.Fatalf("visible=%+v", activation.Visible)
	}
	movement, err := world.Move(ctx, 1, Point{9, 2}, true)
	if err != nil {
		t.Fatal(err)
	}
	if movement.Kind != MoveWalked || movement.To != (Point{3, 2}) {
		t.Fatalf("movement=%+v", movement)
	}
	if len(movement.Observers) != 1 || movement.Observers[0] != 2 {
		t.Fatalf("observers=%v", movement.Observers)
	}
	rotation, err := world.Rotate(ctx, 1, 2048)
	if err != nil || rotation.Player.Direction != 2048 {
		t.Fatalf("rotation=%+v err=%v", rotation, err)
	}
	departure, err := world.Leave(ctx, 1)
	if err != nil || len(departure.Observers) != 1 {
		t.Fatalf("departure=%+v err=%v", departure, err)
	}
	if _, err := world.Move(ctx, 1, Point{}, false); !errors.Is(err, ErrPlayerNotFound) {
		t.Fatalf("expected missing player, got %v", err)
	}
}

func TestWorldLoadsGuardsIntoAOIAndCollision(t *testing.T) {
	world, cancel := startTestWorld(t, 3)
	defer cancel()
	ctx := context.Background()
	_, err := world.Join(ctx, Player{ObjectID: 1, CharacterID: 1, Account: "a", Name: "Observer", MapID: 142, Position: Point{5, 2}})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := world.Activate(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(activation.Guards) != 1 || activation.Guards[0].Template != 7000 {
		t.Fatalf("visible guards=%+v", activation.Guards)
	}
	guard, err := world.GuardForPlayer(ctx, 1, activation.Guards[0].ObjectID)
	if err != nil || guard.Name != "Test Guard" {
		t.Fatalf("guard query=%+v, %v", guard, err)
	}
	maps, players, active, guards := world.Stats(ctx)
	if maps != 1 || players != 1 || active != 1 || guards != 1 {
		t.Fatalf("world stats=%d/%d/%d/%d", maps, players, active, guards)
	}
	movement, err := world.Move(ctx, 1, Point{9, 2}, true)
	if err != nil {
		t.Fatal(err)
	}
	if movement.Kind != MoveStopped || movement.Player.Position != (Point{5, 2}) {
		t.Fatalf("guard did not block movement: %+v", movement)
	}
}

func TestWorldGuardAOITransitions(t *testing.T) {
	world, cancel := startTestWorld(t, 3)
	defer cancel()
	ctx := context.Background()
	_, _ = world.Join(ctx, Player{ObjectID: 1, CharacterID: 1, Account: "a", Name: "Observer", MapID: 142, Position: Point{2, 2}})
	activation, _ := world.Activate(ctx, 1)
	if len(activation.Guards) != 0 {
		t.Fatalf("initial guards=%+v", activation.Guards)
	}
	entered, err := world.Move(ctx, 1, Point{9, 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entered.EnteredGuards) != 1 || entered.EnteredGuards[0].Template != 7000 {
		t.Fatalf("entered guards=%+v", entered.EnteredGuards)
	}
	left, err := world.Move(ctx, 1, Point{0, 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(left.LeftGuards) != 1 || left.LeftGuards[0].Template != 7000 {
		t.Fatalf("left guards=%+v", left.LeftGuards)
	}
	if _, err := world.GuardForPlayer(ctx, 1, entered.EnteredGuards[0].ObjectID); !errors.Is(err, ErrPlayerNotFound) {
		t.Fatalf("out-of-range guard query: %v", err)
	}
}

func TestWorldTeleportAcrossMaps(t *testing.T) {
	world, cancel := startTestWorld(t, 3)
	defer cancel()
	ctx := context.Background()
	players := []Player{
		{ObjectID: 1, CharacterID: 1, Account: "a", Name: "Traveler", MapID: 142, Position: Point{2, 2}, Level: 10},
		{ObjectID: 2, CharacterID: 2, Account: "b", Name: "Old Observer", MapID: 142, Position: Point{4, 2}},
		{ObjectID: 3, CharacterID: 3, Account: "c", Name: "New Observer", MapID: 143, Position: Point{7, 7}},
	}
	for _, player := range players {
		if _, err := world.Join(ctx, player); err != nil {
			t.Fatal(err)
		}
		if _, err := world.Activate(ctx, player.ObjectID); err != nil {
			t.Fatal(err)
		}
	}
	transition, err := world.Teleport(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !transition.MapChanged || transition.Player.MapID != 143 || transition.Player.Position != (Point{6, 6}) || transition.Player.Active {
		t.Fatalf("unexpected transition: %+v", transition)
	}
	if len(transition.OldObservers) != 1 || transition.OldObservers[0] != 2 {
		t.Fatalf("old observers: %v", transition.OldObservers)
	}
	activation, err := world.Activate(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(activation.Visible) != 1 || activation.Visible[0].ObjectID != 3 {
		t.Fatalf("destination visibility: %+v", activation.Visible)
	}
}

func TestWorldSameMapTeleportAndValidation(t *testing.T) {
	world, cancel := startTestWorld(t, 2)
	defer cancel()
	ctx := context.Background()
	for _, player := range []Player{
		{ObjectID: 1, CharacterID: 1, Account: "a", Name: "Traveler", MapID: 142, Position: Point{3, 3}, Level: 10},
		{ObjectID: 2, CharacterID: 2, Account: "b", Name: "Old Observer", MapID: 142, Position: Point{4, 3}},
		{ObjectID: 3, CharacterID: 3, Account: "c", Name: "New Observer", MapID: 142, Position: Point{8, 7}},
	} {
		_, _ = world.Join(ctx, player)
		_, _ = world.Activate(ctx, player.ObjectID)
	}
	transition, err := world.Teleport(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if transition.MapChanged || transition.Player.Position != (Point{8, 8}) || !transition.Player.Active {
		t.Fatalf("unexpected local transition: %+v", transition)
	}
	if len(transition.OldObservers) != 1 || transition.OldObservers[0] != 2 || len(transition.NewVisible) != 1 || transition.NewVisible[0].ObjectID != 3 {
		t.Fatalf("unexpected AOI transition: old=%v new=%+v", transition.OldObservers, transition.NewVisible)
	}
	if _, err := world.Teleport(ctx, 1, 99); !errors.Is(err, ErrGateNotFound) {
		t.Fatalf("missing gate: %v", err)
	}
	if _, err := world.Teleport(ctx, 1, 3); !errors.Is(err, ErrGateTooFar) {
		t.Fatalf("distant gate: %v", err)
	}
}

func TestWorldTeleportEnforcesMinimumLevel(t *testing.T) {
	world, cancel := startTestWorld(t, 2)
	defer cancel()
	ctx := context.Background()
	_, _ = world.Join(ctx, Player{ObjectID: 1, CharacterID: 1, Account: "a", Name: "Low Level", MapID: 142, Position: Point{2, 2}, Level: 1})
	_, _ = world.Activate(ctx, 1)
	if _, err := world.Teleport(ctx, 1, 1); !errors.Is(err, ErrLevelTooLow) {
		t.Fatalf("minimum level: %v", err)
	}
	player, err := world.Player(ctx, 1)
	if err != nil || player.MapID != 142 || player.Position != (Point{2, 2}) {
		t.Fatalf("failed teleport mutated player: %+v, %v", player, err)
	}
}

func TestWorldRejectsBlockedMoveAndTracksAOI(t *testing.T) {
	world, cancel := startTestWorld(t, 1)
	defer cancel()
	ctx := context.Background()
	_, _ = world.Join(ctx, Player{ObjectID: 1, CharacterID: 1, Account: "a", Name: "One", MapID: 142, Position: Point{4, 5}})
	_, _ = world.Join(ctx, Player{ObjectID: 2, CharacterID: 2, Account: "b", Name: "Two", MapID: 142, Position: Point{2, 5}})
	_, _ = world.Activate(ctx, 1)
	_, _ = world.Activate(ctx, 2)
	blocked, err := world.Move(ctx, 1, Point{9, 5}, false)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Kind != MoveStopped || blocked.To != (Point{4, 5}) {
		t.Fatalf("blocked=%+v", blocked)
	}
	moved, err := world.Move(ctx, 2, Point{9, 5}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Entered) != 1 || moved.Entered[0].ObjectID != 1 {
		t.Fatalf("AOI entered=%+v", moved.Entered)
	}
}
