package account

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ResetResult values are wire-compatible with the C# ResetPasswordResult enum.
type ResetResult int

const (
	ResetSuccess ResetResult = iota
	ResetNewPasswordInvalid
	ResetAccountInfoInvalid
)

type Service struct {
	repository Repository
	bcryptCost int
	now        func() time.Time
}

func NewService(repository Repository, bcryptCost int) *Service {
	if bcryptCost == 0 {
		bcryptCost = bcrypt.DefaultCost
	}
	return &Service{repository: repository, bcryptCost: bcryptCost, now: time.Now}
}

func (s *Service) Authenticate(ctx context.Context, name, password string) (bool, error) {
	acc, err := s.repository.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !acc.PasswordEncrypted {
		if acc.Password != password {
			return false, nil
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
		if err != nil {
			return false, fmt.Errorf("upgrade password: %w", err)
		}
		if err := s.repository.UpdatePassword(ctx, acc.Name, string(hash), true); err != nil {
			return false, fmt.Errorf("persist upgraded password: %w", err)
		}
		return true, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(acc.Password), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return false, fmt.Errorf("verify password: %w", err)
	}
	return true, nil
}

func ValidateRegistration(name, password, question, answer string) error {
	switch {
	case len(name) <= 5 || len(name) > 12:
		return errors.New("Username length is wrong")
	case len(password) <= 5 || len(password) > 18:
		return errors.New("Wrong password length")
	case len(question) <= 1 || len(question) > 18:
		return errors.New("Question length is wrong")
	case len(answer) <= 1 || len(answer) > 18:
		return errors.New("Answer length is wrong")
	case !usernamePattern.MatchString(name):
		return errors.New("Username format error")
	default:
		return nil
	}
}

func (s *Service) Register(ctx context.Context, name, password, question, answer string) (Account, error) {
	if err := ValidateRegistration(name, password, question, answer); err != nil {
		return Account{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return Account{}, fmt.Errorf("hash password: %w", err)
	}
	acc := Account{Name: name, PasswordEncrypted: true, Password: string(hash), Question: question, Answer: answer, CreatedAt: s.now().UTC()}
	if err := s.repository.Create(ctx, acc); err != nil {
		return Account{}, err
	}
	return acc, nil
}

func (s *Service) Exists(ctx context.Context, name string) (bool, error) {
	return s.repository.Exists(ctx, name)
}

func (s *Service) ResetPassword(ctx context.Context, name, newPassword, question, answer string) (ResetResult, error) {
	if len(newPassword) < 6 || len(newPassword) > 18 {
		return ResetNewPasswordInvalid, nil
	}
	acc, err := s.repository.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return ResetAccountInfoInvalid, nil
	}
	if err != nil {
		return ResetAccountInfoInvalid, err
	}
	if acc.Question != question || acc.Answer != answer {
		return ResetAccountInfoInvalid, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return ResetAccountInfoInvalid, fmt.Errorf("hash reset password: %w", err)
	}
	if err := s.repository.UpdatePassword(ctx, acc.Name, string(hash), true); err != nil {
		return ResetAccountInfoInvalid, err
	}
	return ResetSuccess, nil
}
