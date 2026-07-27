// Package gameworld owns authoritative map, AOI, and player movement state.
// All mutations are serialized through a single actor loop.
package gameworld

import (
	"errors"
	"math"
)

var (
	ErrNotRunning       = errors.New("game world is not running")
	ErrPlayerExists     = errors.New("player already exists in world")
	ErrPlayerNotFound   = errors.New("player not found in world")
	ErrPlayerNotActive  = errors.New("player is not active in scene")
	ErrInvalidDirection = errors.New("invalid player direction")
	ErrMapFull          = errors.New("map player limit reached")
	ErrGateNotFound     = errors.New("teleport gate not found")
	ErrGateTooFar       = errors.New("player is too far from teleport gate")
	ErrLevelTooLow      = errors.New("player level is below map minimum")
	ErrDestination      = errors.New("teleport destination is unavailable")
)

type Point struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

type Player struct {
	ObjectID    int32
	CharacterID int32
	Account     string
	Name        string
	MapID       int32
	RouteID     int32
	Position    Point
	Altitude    uint16
	Direction   uint16
	Race        byte
	Gender      byte
	Hair        byte
	HairColor   byte
	Face        byte
	Level       byte
	CurrentHP   int32
	MaxHP       int32
	CurrentMP   int32
	MaxMP       int32
	Active      bool
}

type Activation struct {
	Player  Player
	Visible []Player
	Guards  []Guard
}

type Departure struct {
	Player    Player
	Observers []int32
}

type MoveKind uint8

const (
	MoveStopped MoveKind = iota
	MoveWalked
	MoveRan
)

type Movement struct {
	Player        Player
	From          Point
	To            Point
	Kind          MoveKind
	Observers     []int32  // Saw the player both before and after the movement.
	Entered       []Player // Became visible to the moving player.
	Left          []Player // Left the moving player's view.
	EnteredGuards []Guard
	LeftGuards    []Guard
}

type Rotation struct {
	Player    Player
	Observers []int32
}

type Transition struct {
	Player       Player
	Gate         TeleportGate
	FromMapID    int32
	From         Point
	MapChanged   bool
	OldObservers []int32
	OldGuards    []Guard
	NewVisible   []Player
	NewGuards    []Guard
}

func GridDistance(a, b Point) int32 {
	dx, dy := abs(b.X-a.X), abs(b.Y-a.Y)
	if dx > dy {
		return dx
	}
	return dy
}

func Direction(from, to Point) uint16 {
	if from == to {
		return 0
	}
	angle := math.Atan2(float64(to.Y-from.Y), float64(to.X-from.X))*180/math.Pi + 360
	return uint16(int(math.Round(math.Mod(angle, 360)/45)) * 1024 % 8192)
}

func Step(origin Point, direction uint16, distance int32) Point {
	switch direction {
	case 0:
		return Point{X: origin.X + distance, Y: origin.Y}
	case 1024:
		return Point{X: origin.X + distance, Y: origin.Y + distance}
	case 2048:
		return Point{X: origin.X, Y: origin.Y + distance}
	case 3072:
		return Point{X: origin.X - distance, Y: origin.Y + distance}
	case 4096:
		return Point{X: origin.X - distance, Y: origin.Y}
	case 5120:
		return Point{X: origin.X - distance, Y: origin.Y - distance}
	case 6144:
		return Point{X: origin.X, Y: origin.Y - distance}
	case 7168:
		return Point{X: origin.X + distance, Y: origin.Y - distance}
	default:
		return origin
	}
}

func ProtocolPoint(point Point) (uint16, uint16) {
	return uint16(point.X*32 - 16), uint16(point.Y*32 - 16)
}

func GridPoint(x, y uint16) Point {
	return Point{X: int32(math.Round((float64(x) + 16) / 32)), Y: int32(math.Round((float64(y) + 16) / 32))}
}

func validDirection(value uint16) bool { return value <= 7168 && value%1024 == 0 }
func abs(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}
