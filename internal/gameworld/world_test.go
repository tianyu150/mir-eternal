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
