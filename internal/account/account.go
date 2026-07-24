// Package account contains the AccountServer domain and persistence contracts.
package account

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("account not found")
	ErrExists   = errors.New("account already exists")
)

// Account intentionally retains the legacy PasswordEncrypted flag so existing
// C# JSON account files can be opened and upgraded in place on first login.
type Account struct {
	Name              string    `json:"Account"`
	PasswordEncrypted bool      `json:"PasswordEncrypted"`
	Password          string    `json:"Password"`
	Question          string    `json:"Question"`
	Answer            string    `json:"Answer"`
	CreatedAt         time.Time `json:"CreatedDate"`
}

// Repository is the durable boundary used by the account service.
type Repository interface {
	Get(context.Context, string) (Account, error)
	Exists(context.Context, string) (bool, error)
	Create(context.Context, Account) error
	UpdatePassword(context.Context, string, string, bool) error
	Count(context.Context) (int, error)
}
