package accountserver

import "sync/atomic"

type Stats struct {
	accounts      atomic.Int64
	newAccounts   atomic.Int64
	tickets       atomic.Int64
	bytesReceived atomic.Int64
	bytesSent     atomic.Int64
	badPackets    atomic.Int64
}

type StatsSnapshot struct {
	Accounts      int64 `json:"accounts"`
	NewAccounts   int64 `json:"new_accounts"`
	Tickets       int64 `json:"tickets"`
	BytesReceived int64 `json:"bytes_received"`
	BytesSent     int64 `json:"bytes_sent"`
	BadPackets    int64 `json:"bad_packets"`
}

func (s *Stats) Snapshot() StatsSnapshot {
	return StatsSnapshot{Accounts: s.accounts.Load(), NewAccounts: s.newAccounts.Load(), Tickets: s.tickets.Load(), BytesReceived: s.bytesReceived.Load(), BytesSent: s.bytesSent.Load(), BadPackets: s.badPackets.Load()}
}

func (s *Stats) SetAccounts(value int64) { s.accounts.Store(value) }
