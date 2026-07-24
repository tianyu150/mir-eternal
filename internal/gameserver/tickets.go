package gameserver

import (
	"errors"
	"sync"
	"time"
)

var (
	errTicketNotFound = errors.New("login ticket does not exist")
	errTicketExpired  = errors.New("login ticket expired")
	errTicketCapacity = errors.New("login ticket capacity reached")
)

type loginTicket struct {
	Account   string
	ExpiresAt time.Time
}
type ticketStore struct {
	mu      sync.Mutex
	items   map[string]loginTicket
	maximum int
	now     func() time.Time
}

func newTicketStore(maximum int) *ticketStore {
	return &ticketStore{items: make(map[string]loginTicket), maximum: maximum, now: time.Now}
}
func (s *ticketStore) Add(value, account string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	if _, exists := s.items[value]; !exists && len(s.items) >= s.maximum {
		return errTicketCapacity
	}
	s.items[value] = loginTicket{Account: account, ExpiresAt: expiresAt}
	return nil
}
func (s *ticketStore) Consume(value string) (loginTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[value]
	if !ok {
		return loginTicket{}, errTicketNotFound
	}
	delete(s.items, value)
	if !item.ExpiresAt.After(s.now()) {
		return loginTicket{}, errTicketExpired
	}
	return item, nil
}
func (s *ticketStore) cleanupLocked() {
	now := s.now()
	for value, item := range s.items {
		if !item.ExpiresAt.After(now) {
			delete(s.items, value)
		}
	}
}
func (s *ticketStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	return len(s.items)
}
