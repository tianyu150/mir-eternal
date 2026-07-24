package gamestore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCharacterLifecycleAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := store.EnsureAccount(ctx, "Player1"); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateCharacter(ctx, "player1", CreateCharacter{Name: "Hero", Race: 1, Gender: 1, Hair: 2, HairColor: 3, Face: 4})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 1 || created.MapID != 142 || created.Status != Active {
		t.Fatalf("unexpected character: %+v", created)
	}
	if _, err := store.CreateCharacter(ctx, "Player1", CreateCharacter{Name: "hero"}); !errors.Is(err, ErrCharacterExists) {
		t.Fatalf("expected duplicate, got %v", err)
	}
	if _, err := store.FreezeCharacter(ctx, "Player1", created.ID); err != nil {
		t.Fatal(err)
	}
	characters, err := store.ListCharacters(ctx, "Player1")
	if err != nil || len(characters) != 1 || characters[0].Status != Frozen {
		t.Fatalf("list: %+v, %v", characters, err)
	}
	if _, err := store.RestoreCharacter(ctx, "Player1", created.ID); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	characters, err = reopened.ListCharacters(ctx, "PLAYER1")
	if err != nil || len(characters) != 1 || characters[0].Name != "Hero" {
		t.Fatalf("reopened list: %+v, %v", characters, err)
	}
}

func TestPermanentDeleteLimit(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "game.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	ctx := context.Background()
	_, _ = store.EnsureAccount(ctx, "Player1")
	first, _ := store.CreateCharacter(ctx, "Player1", CreateCharacter{Name: "First"})
	_, _ = store.FreezeCharacter(ctx, "Player1", first.ID)
	if _, err := store.DeleteCharacter(ctx, "Player1", first.ID); err != nil {
		t.Fatal(err)
	}
	second, _ := store.CreateCharacter(ctx, "Player1", CreateCharacter{Name: "Second"})
	_, _ = store.FreezeCharacter(ctx, "Player1", second.ID)
	if _, err := store.DeleteCharacter(ctx, "Player1", second.ID); !errors.Is(err, ErrDeleteRestricted) {
		t.Fatalf("expected daily limit, got %v", err)
	}
}
