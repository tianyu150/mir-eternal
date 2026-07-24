package account

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type memoryRepository struct {
	mu sync.Mutex
	m  map[string]Account
}

func newMemoryRepository() *memoryRepository { return &memoryRepository{m: make(map[string]Account)} }
func (r *memoryRepository) Get(_ context.Context, name string) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.m[normalize(name)]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}
func (r *memoryRepository) Exists(_ context.Context, name string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.m[normalize(name)]
	return ok, nil
}
func (r *memoryRepository) Create(_ context.Context, a Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := normalize(a.Name)
	if _, ok := r.m[key]; ok {
		return ErrExists
	}
	r.m[key] = a
	return nil
}
func (r *memoryRepository) UpdatePassword(_ context.Context, name, password string, encrypted bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := normalize(name)
	a, ok := r.m[key]
	if !ok {
		return ErrNotFound
	}
	a.Password = password
	a.PasswordEncrypted = encrypted
	r.m[key] = a
	return nil
}
func (r *memoryRepository) Count(_ context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m), nil
}

func TestRegisterAuthenticateAndReset(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	service := NewService(repo, 4)
	if _, err := service.Register(ctx, "Player_1", "secret1", "pet", "cat"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, "player_1", "secret2", "pet", "dog"); !errors.Is(err, ErrExists) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	ok, err := service.Authenticate(ctx, "PLAYER_1", "secret1")
	if err != nil || !ok {
		t.Fatalf("authenticate: %v, %v", ok, err)
	}
	ok, err = service.Authenticate(ctx, "Player_1", "wrong")
	if err != nil || ok {
		t.Fatalf("wrong password: %v, %v", ok, err)
	}

	result, err := service.ResetPassword(ctx, "player_1", "secret2", "pet", "cat")
	if err != nil || result != ResetSuccess {
		t.Fatalf("reset: %v, %v", result, err)
	}
	ok, err = service.Authenticate(ctx, "Player_1", "secret2")
	if err != nil || !ok {
		t.Fatalf("authenticate reset password: %v, %v", ok, err)
	}
}

func TestPlaintextPasswordIsUpgraded(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	repo.m["legacy"] = Account{Name: "Legacy", Password: "plain", PasswordEncrypted: false}
	service := NewService(repo, 4)
	ok, err := service.Authenticate(ctx, "legacy", "plain")
	if err != nil || !ok {
		t.Fatalf("authenticate: %v, %v", ok, err)
	}
	if !repo.m["legacy"].PasswordEncrypted || repo.m["legacy"].Password == "plain" {
		t.Fatal("legacy password was not upgraded")
	}
}

func TestValidateRegistration(t *testing.T) {
	for _, tc := range []struct{ name, password, question, answer string }{
		{"short", "secret", "q1", "a1"},
		{"bad-name", "secret", "q1", "a1"},
		{"Player1", "tiny", "q1", "a1"},
	} {
		if err := ValidateRegistration(tc.name, tc.password, tc.question, tc.answer); err == nil {
			t.Fatalf("expected validation error for %+v", tc)
		}
	}
	if err := ValidateRegistration("Player1", "secret", "q1", "a1"); err != nil {
		t.Fatal(err)
	}
}
