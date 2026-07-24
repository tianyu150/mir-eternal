package gameworld

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const passableMask uint32 = 0x10000000

type MapSpec struct {
	MapID          int32  `json:"MapId"`
	MapName        string `json:"MapName"`
	TerrainFile    string `json:"TerrainFile"`
	LimitPlayers   int    `json:"LimitPlayers"`
	NoReconnect    bool   `json:"NoReconnect"`
	NoReconnectMap int32  `json:"NoReconnectMapId"`
}

type Terrain struct {
	Start  Point
	End    Point
	Width  int32
	Height int32
	cells  []uint32
}

func (t *Terrain) InBounds(point Point) bool {
	return point.X >= t.Start.X && point.Y >= t.Start.Y && point.X < t.End.X && point.Y < t.End.Y
}
func (t *Terrain) index(point Point) int {
	return int((point.X-t.Start.X)*t.Height + (point.Y - t.Start.Y))
}
func (t *Terrain) CanPass(point Point) bool {
	return t.InBounds(point) && t.cells[t.index(point)]&passableMask == passableMask
}
func (t *Terrain) Altitude(point Point) uint16 {
	if !t.InBounds(point) {
		return 0
	}
	return uint16((t.cells[t.index(point)] & 0xffff) - 30)
}

type MapData struct {
	Spec         MapSpec
	Terrain      *Terrain
	Resurrection []Point
}

type Catalog struct {
	root     string
	maxCells int64
	specs    map[int32]MapSpec
	mu       sync.Mutex
	loaded   map[int32]*MapData
}

func OpenCatalog(systemPath string, maxTerrainCells int64) (*Catalog, error) {
	if systemPath == "" {
		return nil, errors.New("system data path is empty")
	}
	if maxTerrainCells <= 0 {
		maxTerrainCells = 16_000_000
	}
	mapsPath := filepath.Join(systemPath, "GameMap", "Maps")
	entries, err := os.ReadDir(mapsPath)
	if err != nil {
		return nil, fmt.Errorf("read map definitions: %w", err)
	}
	catalog := &Catalog{root: systemPath, maxCells: maxTerrainCells, specs: make(map[int32]MapSpec), loaded: make(map[int32]*MapData)}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(mapsPath, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read map definition %q: %w", entry.Name(), err)
		}
		var spec MapSpec
		payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
		if err := json.Unmarshal(payload, &spec); err != nil {
			return nil, fmt.Errorf("decode map definition %q: %w", entry.Name(), err)
		}
		if spec.MapID <= 0 {
			return nil, fmt.Errorf("map definition %q has invalid ID", entry.Name())
		}
		if spec.LimitPlayers <= 0 {
			spec.LimitPlayers = 9999
		}
		if _, exists := catalog.specs[spec.MapID]; exists {
			return nil, fmt.Errorf("duplicate map ID %d", spec.MapID)
		}
		catalog.specs[spec.MapID] = spec
	}
	if len(catalog.specs) == 0 {
		return nil, errors.New("no map definitions found")
	}
	return catalog, nil
}

func (c *Catalog) Load(mapID int32) (*MapData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if loaded := c.loaded[mapID]; loaded != nil {
		return loaded, nil
	}
	spec, ok := c.specs[mapID]
	if !ok {
		return nil, fmt.Errorf("map %d is not defined", mapID)
	}
	terrain, err := c.loadTerrain(mapID)
	if err != nil {
		return nil, err
	}
	areas, err := c.loadResurrectionAreas(mapID)
	if err != nil {
		return nil, err
	}
	result := &MapData{Spec: spec, Terrain: terrain, Resurrection: areas}
	c.loaded[mapID] = result
	return result, nil
}

func (c *Catalog) loadTerrain(mapID int32) (*Terrain, error) {
	pattern := filepath.Join(c.root, "GameMap", "Terrains", fmt.Sprintf("%04d-*", mapID)+".terrain")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) != 1 {
		return nil, fmt.Errorf("map %d terrain: expected one file, found %d", mapID, len(matches))
	}
	file, err := os.Open(matches[0])
	if err != nil {
		return nil, fmt.Errorf("open terrain for map %d: %w", mapID, err)
	}
	defer file.Close()
	var header [6]int32
	for index := range header {
		if err := binary.Read(file, binary.LittleEndian, &header[index]); err != nil {
			return nil, fmt.Errorf("read terrain header for map %d: %w", mapID, err)
		}
	}
	width, height := int64(header[2]-header[0]), int64(header[3]-header[1])
	if width <= 0 || height <= 0 || width*height > c.maxCells {
		return nil, fmt.Errorf("map %d terrain dimensions %dx%d are invalid", mapID, width, height)
	}
	cells := make([]uint32, width*height)
	if err := binary.Read(file, binary.LittleEndian, cells); err != nil {
		return nil, fmt.Errorf("read terrain cells for map %d: %w", mapID, err)
	}
	var extra [1]byte
	if n, err := file.Read(extra[:]); n != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return nil, fmt.Errorf("map %d terrain has trailing data", mapID)
	}
	return &Terrain{Start: Point{X: header[0], Y: header[1]}, End: Point{X: header[2], Y: header[3]}, Width: int32(width), Height: int32(height), cells: cells}, nil
}

func (c *Catalog) loadResurrectionAreas(mapID int32) ([]Point, error) {
	pattern := filepath.Join(c.root, "GameMap", "MapAreas", strconv.Itoa(int(mapID))+"-*.txt")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	for _, path := range matches {
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var area struct {
			AreaType    string   `json:"AreaType"`
			FromCoords  string   `json:"FromCoords"`
			RangeCoords []string `json:"RangeCoords"`
		}
		payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
		if err := json.Unmarshal(payload, &area); err != nil {
			return nil, fmt.Errorf("decode map area %q: %w", filepath.Base(path), err)
		}
		if area.AreaType != "复活区域" {
			continue
		}
		points := make([]Point, 0, len(area.RangeCoords)+1)
		for _, raw := range area.RangeCoords {
			if point, err := parsePoint(raw); err == nil {
				points = append(points, point)
			}
		}
		if point, err := parsePoint(area.FromCoords); err == nil {
			points = append(points, point)
		}
		return points, nil
	}
	return nil, nil
}

func parsePoint(value string) (Point, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return Point{}, errors.New("invalid point")
	}
	x, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return Point{}, err
	}
	y, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return Point{}, err
	}
	return Point{X: int32(x), Y: int32(y)}, nil
}
