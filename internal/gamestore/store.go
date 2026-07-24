// Package gamestore provides the persistent account/character selector data
// used by the Go GameServer. World-state migration will build on this boundary.
package gamestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type CharacterStatus string

const (
	Active  CharacterStatus = "active"
	Frozen  CharacterStatus = "frozen"
	Deleted CharacterStatus = "deleted"
)

var (
	ErrAccountNotFound   = errors.New("game account not found")
	ErrCharacterNotFound = errors.New("character not found")
	ErrCharacterExists   = errors.New("character name already exists")
	ErrActiveSlotsFull   = errors.New("active character slots are full")
	ErrFrozenSlotsFull   = errors.New("frozen character slots are full")
	ErrDeleteRestricted  = errors.New("character cannot be permanently deleted")
	ErrInvalidCharacter  = errors.New("invalid character")
)

type Account struct {
	Name           string    `json:"name"`
	BanUntil       time.Time `json:"ban_until,omitempty"`
	LastDeleteDate time.Time `json:"last_delete_date,omitempty"`
}

type Character struct {
	ID        int32           `json:"id"`
	Account   string          `json:"account"`
	Name      string          `json:"name"`
	Gender    byte            `json:"gender"`
	Race      byte            `json:"race"`
	Hair      byte            `json:"hair"`
	HairColor byte            `json:"hair_color"`
	Face      byte            `json:"face"`
	Level     byte            `json:"level"`
	MapID     int32           `json:"map_id"`
	PositionX int32           `json:"position_x"`
	PositionY int32           `json:"position_y"`
	Direction uint16          `json:"direction"`
	CurrentHP int32           `json:"current_hp"`
	CurrentMP int32           `json:"current_mp"`
	Status    CharacterStatus `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	OfflineAt time.Time       `json:"offline_at"`
	FrozenAt  time.Time       `json:"frozen_at,omitempty"`
	DeletedAt time.Time       `json:"deleted_at,omitempty"`
	BanUntil  time.Time       `json:"ban_until,omitempty"`
}

type CreateCharacter struct {
	Name                                string
	Gender, Race, Hair, HairColor, Face byte
}

type database struct {
	Version         int                 `json:"version"`
	NextCharacterID int32               `json:"next_character_id"`
	Accounts        map[string]Account  `json:"accounts"`
	Characters      map[int32]Character `json:"characters"`
	Names           map[string]int32    `json:"names"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data database
	now  func() time.Time
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("game database path is empty")
	}
	s := &Store{path: path, now: time.Now, data: database{Version: 1, NextCharacterID: 1, Accounts: make(map[string]Account), Characters: make(map[int32]Character), Names: make(map[string]int32)}}
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create game database directory: %w", err)
		}
		if err := s.persist(s.data); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read game database: %w", err)
	}
	if err := json.Unmarshal(payload, &s.data); err != nil {
		return nil, fmt.Errorf("decode game database: %w", err)
	}
	if s.data.Version != 1 {
		return nil, fmt.Errorf("unsupported game database version %d", s.data.Version)
	}
	if s.data.Accounts == nil {
		s.data.Accounts = make(map[string]Account)
	}
	if s.data.Characters == nil {
		s.data.Characters = make(map[int32]Character)
	}
	if s.data.Names == nil {
		s.data.Names = make(map[string]int32)
	}
	if s.data.NextCharacterID < 1 {
		s.data.NextCharacterID = 1
	}
	// Version-1 snapshots created before world-state migration have zero-valued
	// progression fields. Upgrade them in memory without changing valid data.
	for id, character := range s.data.Characters {
		if character.Level == 0 {
			character.Level = 1
		}
		if character.CurrentHP <= 0 {
			character.CurrentHP = 100
		}
		if character.CurrentMP < 0 {
			character.CurrentMP = 0
		}
		s.data.Characters[id] = character
	}
	return s, nil
}

func normalize(value string) string { return strings.ToLower(value) }

func (s *Store) mutate(fn func(*database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := clone(s.data)
	if err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func clone(source database) (database, error) {
	payload, err := json.Marshal(source)
	if err != nil {
		return database{}, err
	}
	var result database
	if err := json.Unmarshal(payload, &result); err != nil {
		return database{}, err
	}
	return result, nil
}

func (s *Store) persist(data database) error {
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode game database: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create game database directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".game-*.tmp")
	if err != nil {
		return fmt.Errorf("create game database transaction: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write game database transaction: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync game database transaction: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("commit game database transaction: %w", err)
	}
	return nil
}

func (s *Store) EnsureAccount(_ context.Context, name string) (Account, error) {
	var result Account
	err := s.mutate(func(data *database) error {
		key := normalize(name)
		if existing, ok := data.Accounts[key]; ok {
			result = existing
			return nil
		}
		result = Account{Name: name}
		data.Accounts[key] = result
		return nil
	})
	return result, err
}

func (s *Store) GetAccount(_ context.Context, name string) (Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data.Accounts[normalize(name)]
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	return value, nil
}

func (s *Store) ListCharacters(_ context.Context, account string) ([]Character, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.data.Accounts[normalize(account)]; !ok {
		return nil, ErrAccountNotFound
	}
	result := make([]Character, 0)
	for _, character := range s.data.Characters {
		if strings.EqualFold(character.Account, account) && character.Status != Deleted {
			result = append(result, character)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status != result[j].Status {
			return result[i].Status == Active
		}
		if result[i].Level != result[j].Level {
			return result[i].Level > result[j].Level
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func validateCharacterName(name string) error {
	if name != strings.TrimSpace(name) || name == "" || !utf8.ValidString(name) || len([]byte(name)) > 24 {
		return ErrInvalidCharacter
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == ';' {
			return ErrInvalidCharacter
		}
	}
	return nil
}

func (s *Store) CreateCharacter(_ context.Context, account string, request CreateCharacter) (Character, error) {
	if err := validateCharacterName(request.Name); err != nil {
		return Character{}, err
	}
	var result Character
	err := s.mutate(func(data *database) error {
		accountData, ok := data.Accounts[normalize(account)]
		if !ok {
			return ErrAccountNotFound
		}
		if _, ok := data.Names[normalize(request.Name)]; ok {
			return ErrCharacterExists
		}
		active := 0
		for _, c := range data.Characters {
			if strings.EqualFold(c.Account, accountData.Name) && c.Status == Active {
				active++
			}
		}
		if active >= 4 {
			return ErrActiveSlotsFull
		}
		now := s.now().UTC()
		result = Character{ID: data.NextCharacterID, Account: accountData.Name, Name: request.Name, Gender: request.Gender, Race: request.Race, Hair: request.Hair, HairColor: request.HairColor, Face: request.Face, Level: 1, MapID: 142, CurrentHP: 100, CurrentMP: 100, Status: Active, CreatedAt: now, OfflineAt: now}
		data.NextCharacterID++
		data.Characters[result.ID] = result
		data.Names[normalize(result.Name)] = result.ID
		return nil
	})
	return result, err
}

func (s *Store) characterMutation(account string, id int32, valid CharacterStatus, fn func(*database, Account, Character) (Character, error)) (Character, error) {
	var result Character
	err := s.mutate(func(data *database) error {
		a, ok := data.Accounts[normalize(account)]
		if !ok {
			return ErrAccountNotFound
		}
		c, ok := data.Characters[id]
		if !ok || !strings.EqualFold(c.Account, a.Name) || c.Status != valid {
			return ErrCharacterNotFound
		}
		var err error
		result, err = fn(data, a, c)
		return err
	})
	return result, err
}

func (s *Store) FreezeCharacter(_ context.Context, account string, id int32) (Character, error) {
	return s.characterMutation(account, id, Active, func(data *database, a Account, c Character) (Character, error) {
		frozen := 0
		for _, other := range data.Characters {
			if strings.EqualFold(other.Account, a.Name) && other.Status == Frozen {
				frozen++
			}
		}
		if frozen >= 5 {
			return Character{}, ErrFrozenSlotsFull
		}
		c.Status = Frozen
		c.FrozenAt = s.now().UTC()
		data.Characters[id] = c
		return c, nil
	})
}
func (s *Store) RestoreCharacter(_ context.Context, account string, id int32) (Character, error) {
	return s.characterMutation(account, id, Frozen, func(data *database, a Account, c Character) (Character, error) {
		active := 0
		for _, other := range data.Characters {
			if strings.EqualFold(other.Account, a.Name) && other.Status == Active {
				active++
			}
		}
		if active >= 4 {
			return Character{}, ErrActiveSlotsFull
		}
		c.Status = Active
		c.FrozenAt = time.Time{}
		data.Characters[id] = c
		return c, nil
	})
}
func (s *Store) DeleteCharacter(_ context.Context, account string, id int32) (Character, error) {
	return s.characterMutation(account, id, Frozen, func(data *database, a Account, c Character) (Character, error) {
		now := s.now().UTC()
		if c.Level >= 40 || (!a.LastDeleteDate.IsZero() && a.LastDeleteDate.Year() == now.Year() && a.LastDeleteDate.YearDay() == now.YearDay()) {
			return Character{}, ErrDeleteRestricted
		}
		c.Status = Deleted
		c.DeletedAt = now
		a.LastDeleteDate = now
		data.Characters[id] = c
		data.Accounts[normalize(a.Name)] = a
		delete(data.Names, normalize(c.Name))
		return c, nil
	})
}

type WorldState struct {
	MapID     int32
	PositionX int32
	PositionY int32
	Direction uint16
	CurrentHP int32
	CurrentMP int32
}

// SaveWorldState persists the authoritative world state when a character
// leaves a session. Movement remains in-memory during play to avoid fsync on
// every step.
func (s *Store) SaveWorldState(_ context.Context, account string, id int32, state WorldState) error {
	return s.mutate(func(data *database) error {
		character, ok := data.Characters[id]
		if !ok || !strings.EqualFold(character.Account, account) || character.Status != Active {
			return ErrCharacterNotFound
		}
		character.MapID = state.MapID
		character.PositionX = state.PositionX
		character.PositionY = state.PositionY
		character.Direction = state.Direction
		character.CurrentHP = state.CurrentHP
		character.CurrentMP = state.CurrentMP
		character.OfflineAt = s.now().UTC()
		data.Characters[id] = character
		return nil
	})
}

func (s *Store) ActiveCharacter(_ context.Context, account string, id int32) (Character, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data.Characters[id]
	if !ok || !strings.EqualFold(c.Account, account) || c.Status != Active {
		return Character{}, ErrCharacterNotFound
	}
	return c, nil
}

func (s *Store) Counts() (accounts, characters int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data.Accounts), len(s.data.Characters)
}
