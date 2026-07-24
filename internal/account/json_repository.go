package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// JSONRepository is compatible with AccountServer/Repositories/JSON. Writes
// use a temporary file and atomic rename, and all map/file mutations are
// serialized to avoid the races present in the original implementation.
type JSONRepository struct {
	dir      string
	mu       sync.RWMutex
	accounts map[string]Account
	paths    map[string]string
}

// legacyAccount uses a string date because Newtonsoft's DateTime.MinValue
// (0001-01-01T00:00:00) is not RFC3339. This also makes old files lossless.
type legacyAccount struct {
	Account           string `json:"Account"`
	PasswordEncrypted bool   `json:"PasswordEncrypted"`
	Password          string `json:"Password"`
	Question          string `json:"Question"`
	Answer            string `json:"Answer"`
	CreatedDate       string `json:"CreatedDate,omitempty"`
}

func OpenJSONRepository(dir string) (*JSONRepository, error) {
	if dir == "" {
		return nil, errors.New("account data directory is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create account data directory: %w", err)
	}
	r := &JSONRepository{dir: dir, accounts: make(map[string]Account), paths: make(map[string]string)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read account data directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".txt" && filepath.Ext(entry.Name()) != ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read account file %q: %w", entry.Name(), err)
		}
		var disk legacyAccount
		if err := json.Unmarshal(data, &disk); err != nil {
			return nil, fmt.Errorf("decode account file %q: %w", entry.Name(), err)
		}
		acc, err := disk.domain()
		if err != nil {
			return nil, fmt.Errorf("decode account file %q: %w", entry.Name(), err)
		}
		key := normalize(acc.Name)
		if _, exists := r.accounts[key]; exists {
			return nil, fmt.Errorf("duplicate account %q", acc.Name)
		}
		r.accounts[key] = acc
		r.paths[key] = filepath.Join(dir, entry.Name())
	}
	return r, nil
}

func (d legacyAccount) domain() (Account, error) {
	if d.Account == "" || d.Password == "" {
		return Account{}, errors.New("account and password are required")
	}
	created := time.Time{}
	if d.CreatedDate != "" && !strings.HasPrefix(d.CreatedDate, "0001-01-01") {
		var err error
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05"} {
			created, err = time.Parse(layout, d.CreatedDate)
			if err == nil {
				break
			}
		}
		if err != nil {
			return Account{}, fmt.Errorf("invalid CreatedDate: %w", err)
		}
	}
	return Account{Name: d.Account, PasswordEncrypted: d.PasswordEncrypted, Password: d.Password, Question: d.Question, Answer: d.Answer, CreatedAt: created}, nil
}

func diskAccount(a Account) legacyAccount {
	date := "0001-01-01T00:00:00"
	if !a.CreatedAt.IsZero() {
		date = a.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return legacyAccount{Account: a.Name, PasswordEncrypted: a.PasswordEncrypted, Password: a.Password, Question: a.Question, Answer: a.Answer, CreatedDate: date}
}

func normalize(name string) string { return strings.ToLower(name) }

func (r *JSONRepository) Get(_ context.Context, name string) (Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	acc, ok := r.accounts[normalize(name)]
	if !ok {
		return Account{}, ErrNotFound
	}
	return acc, nil
}

func (r *JSONRepository) Exists(_ context.Context, name string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.accounts[normalize(name)]
	return ok, nil
}

func (r *JSONRepository) Count(_ context.Context) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.accounts), nil
}

func (r *JSONRepository) Create(_ context.Context, acc Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := normalize(acc.Name)
	if _, ok := r.accounts[key]; ok {
		return ErrExists
	}
	if err := r.writeLocked(acc); err != nil {
		return err
	}
	r.accounts[key] = acc
	r.paths[key] = filepath.Join(r.dir, key+".txt")
	return nil
}

func (r *JSONRepository) UpdatePassword(_ context.Context, name, password string, encrypted bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := normalize(name)
	acc, ok := r.accounts[key]
	if !ok {
		return ErrNotFound
	}
	acc.Password = password
	acc.PasswordEncrypted = encrypted
	if err := r.writeLocked(acc); err != nil {
		return err
	}
	r.accounts[key] = acc
	return nil
}

func (r *JSONRepository) writeLocked(acc Account) error {
	payload, err := json.MarshalIndent(diskAccount(acc), "", "  ")
	if err != nil {
		return fmt.Errorf("encode account: %w", err)
	}
	// Account names accepted by the protocol cannot contain path separators.
	// Lower-case filenames also prevent case-only duplicates on Linux.
	path := r.paths[normalize(acc.Name)]
	if path == "" {
		path = filepath.Join(r.dir, normalize(acc.Name)+".txt")
	}
	tmp, err := os.CreateTemp(r.dir, ".account-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary account file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set account file permissions: %w", err)
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write account file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync account file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close account file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace account file: %w", err)
	}
	// Best effort directory fsync gives rename durability on Unix. Some
	// platforms reject syncing directories, so unsupported errors are ignored.
	if dir, err := os.Open(r.dir); err == nil {
		if err := dir.Sync(); err != nil && !errors.Is(err, fs.ErrInvalid) {
			_ = dir.Close()
			return fmt.Errorf("sync account directory: %w", err)
		}
		_ = dir.Close()
	}
	return nil
}
