package gameserver

import (
	"errors"
	"testing"
	"time"
)

func TestTicketIsOneTimeAndExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := newTicketStore(2)
	store.now = func() time.Time { return now }
	if err := store.Add("a", "Alice", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Consume("a")
	if err != nil || got.Account != "Alice" {
		t.Fatalf("consume: %+v, %v", got, err)
	}
	if _, err := store.Consume("a"); !errors.Is(err, errTicketNotFound) {
		t.Fatalf("ticket reused: %v", err)
	}
	_ = store.Add("old", "Alice", now)
	if _, err := store.Consume("old"); !errors.Is(err, errTicketExpired) && !errors.Is(err, errTicketNotFound) {
		t.Fatalf("expired ticket: %v", err)
	}
}
