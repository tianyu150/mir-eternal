package gameserver

import "sync/atomic"

type Stats struct {
	connections      atomic.Int64
	authenticated    atomic.Int64
	tickets          atomic.Int64
	bytesReceived    atomic.Int64
	bytesSent        atomic.Int64
	packetsReceived  atomic.Int64
	packetsSent      atomic.Int64
	unhandledPackets atomic.Int64
	rejected         atomic.Int64
}
type StatsSnapshot struct {
	Connections      int64 `json:"connections"`
	Authenticated    int64 `json:"authenticated"`
	Tickets          int64 `json:"tickets"`
	BytesReceived    int64 `json:"bytes_received"`
	BytesSent        int64 `json:"bytes_sent"`
	PacketsReceived  int64 `json:"packets_received"`
	PacketsSent      int64 `json:"packets_sent"`
	UnhandledPackets int64 `json:"unhandled_packets"`
	Rejected         int64 `json:"rejected"`
	PendingTickets   int   `json:"pending_tickets"`
	Accounts         int   `json:"accounts"`
	Characters       int   `json:"characters"`
	LoadedMaps       int   `json:"loaded_maps"`
	WorldPlayers     int   `json:"world_players"`
	ActivePlayers    int   `json:"active_players"`
}
