// Package ticket implements the AccountServer-to-GameServer login ticket protocol.
package ticket

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	prefix       = "ULS21-"
	randomLength = 32
)

var (
	ErrMalformed        = errors.New("malformed ticket message")
	ErrInvalidSignature = errors.New("invalid ticket signature")
	ErrExpired          = errors.New("ticket message expired")
)

// Message is sent over the internal UDP ticket channel. ExpiresAt is populated
// only by the authenticated v1 format; legacy messages receive the configured
// TTL when accepted by the GameServer.
type Message struct {
	Ticket    string
	Account   string
	ExpiresAt time.Time
	Signed    bool
}

// Codec supports the legacy "ticket;account" wire format and an authenticated
// v1 format. Setting Secret makes Encode emit v1 and Decode reject legacy data.
type Codec struct {
	Secret []byte
	Now    func() time.Time
}

// Generate returns a cryptographically random ticket accepted by the original
// client (ULS21- followed by 32 alphanumeric characters).
func Generate() (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	out := make([]byte, randomLength)
	buf := make([]byte, randomLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate ticket: %w", err)
	}
	// Rejection sampling avoids modulo bias.
	for i := range out {
		v := buf[i]
		for v >= 248 { // 248 is the largest multiple of 62 below 256.
			if _, err := rand.Read(buf[i : i+1]); err != nil {
				return "", fmt.Errorf("generate ticket: %w", err)
			}
			v = buf[i]
		}
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return prefix + string(out), nil
}

func (c Codec) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Encode serializes a ticket for the internal UDP channel. The client-facing
// ticket is unchanged when authenticated transport is enabled.
func (c Codec) Encode(ticket, account string, expiresAt time.Time) ([]byte, error) {
	if !validField(ticket) || !validField(account) {
		return nil, ErrMalformed
	}
	if len(c.Secret) == 0 {
		return []byte(ticket + ";" + account), nil
	}
	expires := strconv.FormatInt(expiresAt.UTC().Unix(), 10)
	body := strings.Join([]string{"v1", ticket, account, expires}, ";")
	mac := hmac.New(sha256.New, c.Secret)
	_, _ = mac.Write([]byte(body))
	return []byte(body + ";" + hex.EncodeToString(mac.Sum(nil))), nil
}

// Decode verifies and parses an internal ticket message. If Secret is set,
// unsigned legacy messages are rejected to prevent ticket injection.
func (c Codec) Decode(data []byte) (Message, error) {
	parts := strings.Split(string(data), ";")
	if len(parts) == 2 && len(c.Secret) == 0 {
		if !validField(parts[0]) || !validField(parts[1]) {
			return Message{}, ErrMalformed
		}
		return Message{Ticket: parts[0], Account: parts[1]}, nil
	}
	if len(parts) != 5 || parts[0] != "v1" || len(c.Secret) == 0 {
		return Message{}, ErrMalformed
	}
	if !validField(parts[1]) || !validField(parts[2]) {
		return Message{}, ErrMalformed
	}
	body := strings.Join(parts[:4], ";")
	provided, err := hex.DecodeString(parts[4])
	if err != nil {
		return Message{}, ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, c.Secret)
	_, _ = mac.Write([]byte(body))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return Message{}, ErrInvalidSignature
	}
	unix, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return Message{}, ErrMalformed
	}
	expiresAt := time.Unix(unix, 0).UTC()
	if !expiresAt.After(c.now()) {
		return Message{}, ErrExpired
	}
	return Message{Ticket: parts[1], Account: parts[2], ExpiresAt: expiresAt, Signed: true}, nil
}

func validField(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, ";\r\n\x00")
}
