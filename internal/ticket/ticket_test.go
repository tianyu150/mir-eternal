package ticket

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len("ULS21-")+32 || !strings.HasPrefix(a, "ULS21-") {
		t.Fatalf("unexpected ticket %q", a)
	}
	if a == b {
		t.Fatal("two generated tickets are equal")
	}
}

func TestCodecSignedRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	codec := Codec{Secret: []byte("test-secret"), Now: func() time.Time { return now }}
	encoded, err := codec.Encode("ULS21-ticket", "Alice", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	message, err := codec.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if message.Ticket != "ULS21-ticket" || message.Account != "Alice" || !message.Signed {
		t.Fatalf("unexpected message: %+v", message)
	}

	encoded[len(encoded)-1] ^= 1
	if _, err := codec.Decode(encoded); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected invalid signature, got %v", err)
	}
}

func TestCodecCompatibilityAndExpiry(t *testing.T) {
	legacy := Codec{}
	message, err := legacy.Decode([]byte("ticket;account"))
	if err != nil || message.Account != "account" {
		t.Fatalf("legacy decode: %+v, %v", message, err)
	}
	if _, err := (Codec{Secret: []byte("required")}).Decode([]byte("ticket;account")); err == nil {
		t.Fatal("signed codec accepted a legacy message")
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	codec := Codec{Secret: []byte("secret"), Now: func() time.Time { return now }}
	encoded, _ := codec.Encode("ticket", "account", now)
	if _, err := codec.Decode(encoded); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
}
