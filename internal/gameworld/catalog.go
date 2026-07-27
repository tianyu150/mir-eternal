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
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	passableMask          uint32 = 0x10000000
	staticGuardObjectBase int32  = 1_500_000_000
)

var trailingCommaRE = regexp.MustCompile(`,\s*([}\]])`)

type MapSpec struct {
	MapID          int32  `json:"MapId"`
	MapName        string `json:"MapName"`
	TerrainFile    string `json:"TerrainFile"`
	LimitPlayers   int    `json:"LimitPlayers"`
	MinLevel       byte   `json:"MinLevel"`
	NoReconnect    bool   `json:"NoReconnect"`
	NoReconnectMap int32  `json:"NoReconnectMapId"`
}

type TeleportGate struct {
	Number    int32
	Name      string
	FromMapID int32
	ToMapID   int32
	From      Point
	To        Point
}

type Guard struct {
	ObjectID  int32
	Template  uint16
	Name      string
	Level     byte
	MapID     int32
	Position  Point
	Altitude  uint16
	Direction uint16
	MaxHP     int32
	Blocking  bool
}

type gateKey struct {
	mapID  int32
	number int32
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
	Guards       []Guard
}

type Catalog struct {
	root        string
	maxCells    int64
	specs       map[int32]MapSpec
	gates       map[gateKey]TeleportGate
	guardsByMap map[int32][]Guard
	guardsByID  map[int32]Guard
	mu          sync.Mutex
	loaded      map[int32]*MapData
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
	catalog := &Catalog{root: systemPath, maxCells: maxTerrainCells, specs: make(map[int32]MapSpec), gates: make(map[gateKey]TeleportGate), guardsByMap: make(map[int32][]Guard), guardsByID: make(map[int32]Guard), loaded: make(map[int32]*MapData)}
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
	if err := catalog.loadTeleportGates(); err != nil {
		return nil, err
	}
	if err := catalog.loadGuards(); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (c *Catalog) loadTeleportGates() error {
	path := filepath.Join(c.root, "GameMap", "TeleportGates")
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read teleport gate definitions: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return fmt.Errorf("read teleport gate %q: %w", entry.Name(), err)
		}
		var definition struct {
			Number     int32  `json:"TeleportGateNumber"`
			Name       string `json:"TeleportGateName"`
			FromMapID  int32  `json:"FromMapId"`
			ToMapID    int32  `json:"ToMapId"`
			FromCoords string `json:"FromCoords"`
			ToCoords   string `json:"ToCoords"`
		}
		payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
		if err := json.Unmarshal(payload, &definition); err != nil {
			return fmt.Errorf("decode teleport gate %q: %w", entry.Name(), err)
		}
		if definition.Number <= 0 {
			return fmt.Errorf("teleport gate %q has invalid number", entry.Name())
		}
		if _, ok := c.specs[definition.FromMapID]; !ok {
			return fmt.Errorf("teleport gate %q references undefined source map %d", entry.Name(), definition.FromMapID)
		}
		if _, ok := c.specs[definition.ToMapID]; !ok {
			return fmt.Errorf("teleport gate %q references undefined destination map %d", entry.Name(), definition.ToMapID)
		}
		from, err := parsePoint(definition.FromCoords)
		if err != nil {
			return fmt.Errorf("teleport gate %q source coordinates: %w", entry.Name(), err)
		}
		to, err := parsePoint(definition.ToCoords)
		if err != nil {
			return fmt.Errorf("teleport gate %q destination coordinates: %w", entry.Name(), err)
		}
		key := gateKey{mapID: definition.FromMapID, number: definition.Number}
		if _, exists := c.gates[key]; exists {
			return fmt.Errorf("duplicate teleport gate %d on map %d", definition.Number, definition.FromMapID)
		}
		c.gates[key] = TeleportGate{Number: definition.Number, Name: definition.Name, FromMapID: definition.FromMapID, ToMapID: definition.ToMapID, From: from, To: to}
	}
	return nil
}

func (c *Catalog) Gate(mapID, number int32) (TeleportGate, bool) {
	gate, ok := c.gates[gateKey{mapID: mapID, number: number}]
	return gate, ok
}

func (c *Catalog) loadGuards() error {
	templatesPath := filepath.Join(c.root, "Npc", "Guards")
	templateEntries, err := os.ReadDir(templatesPath)
	if err != nil {
		return fmt.Errorf("read guard templates: %w", err)
	}
	type guardTemplate struct {
		Name        string `json:"Name"`
		Number      uint16 `json:"GuardNumber"`
		Level       byte   `json:"Level"`
		Nothingness bool   `json:"Nothingness"`
	}
	templates := make(map[uint16]guardTemplate)
	for _, entry := range templateEntries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(templatesPath, entry.Name()))
		if err != nil {
			return fmt.Errorf("read guard template %q: %w", entry.Name(), err)
		}
		var template guardTemplate
		if err := decodeLegacyJSON(payload, &template); err != nil {
			return fmt.Errorf("decode guard template %q: %w", entry.Name(), err)
		}
		if template.Number == 0 {
			return fmt.Errorf("guard template %q has invalid number", entry.Name())
		}
		if _, exists := templates[template.Number]; exists {
			return fmt.Errorf("duplicate guard template %d", template.Number)
		}
		templates[template.Number] = template
	}

	placementsPath := filepath.Join(c.root, "GameMap", "Guards")
	placementEntries, err := os.ReadDir(placementsPath)
	if err != nil {
		return fmt.Errorf("read guard placements: %w", err)
	}
	ordinal := int32(0)
	for _, entry := range placementEntries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(placementsPath, entry.Name()))
		if err != nil {
			return fmt.Errorf("read guard placement %q: %w", entry.Name(), err)
		}
		var placement struct {
			Template  uint16 `json:"GuardNumber"`
			MapID     int32  `json:"FromMapId"`
			Coords    string `json:"FromCoords"`
			Direction string `json:"Direction"`
		}
		if err := decodeLegacyJSON(payload, &placement); err != nil {
			return fmt.Errorf("decode guard placement %q: %w", entry.Name(), err)
		}
		template, ok := templates[placement.Template]
		if !ok {
			return fmt.Errorf("guard placement %q references undefined template %d", entry.Name(), placement.Template)
		}
		if _, ok := c.specs[placement.MapID]; !ok {
			return fmt.Errorf("guard placement %q references undefined map %d", entry.Name(), placement.MapID)
		}
		position, err := parsePoint(placement.Coords)
		if err != nil {
			return fmt.Errorf("guard placement %q coordinates: %w", entry.Name(), err)
		}
		direction, err := parseDirection(placement.Direction)
		if err != nil {
			return fmt.Errorf("guard placement %q direction: %w", entry.Name(), err)
		}
		ordinal++
		guard := Guard{ObjectID: staticGuardObjectBase + ordinal, Template: placement.Template, Name: template.Name, Level: template.Level, MapID: placement.MapID, Position: position, Direction: direction, MaxHP: 9999, Blocking: !template.Nothingness}
		c.guardsByMap[placement.MapID] = append(c.guardsByMap[placement.MapID], guard)
		c.guardsByID[guard.ObjectID] = guard
	}
	return nil
}

func decodeLegacyJSON(payload []byte, target any) error {
	payload = bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
	payload = trailingCommaRE.ReplaceAll(payload, []byte("$1"))
	return json.Unmarshal(payload, target)
}

func parseDirection(value string) (uint16, error) {
	if value == "" {
		return 0, nil
	}
	directions := map[string]uint16{"左方": 0, "左上": 1024, "上方": 2048, "右上": 3072, "右方": 4096, "右下": 5120, "下方": 6144, "左下": 7168}
	direction, ok := directions[value]
	if !ok {
		return 0, fmt.Errorf("unknown direction %q", value)
	}
	return direction, nil
}

func (c *Catalog) Guard(objectID int32) (Guard, bool) {
	guard, ok := c.guardsByID[objectID]
	return guard, ok
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
	guards := append([]Guard(nil), c.guardsByMap[mapID]...)
	for index := range guards {
		if !terrain.InBounds(guards[index].Position) {
			return nil, fmt.Errorf("guard %d is outside map %d terrain", guards[index].Template, mapID)
		}
		guards[index].Altitude = terrain.Altitude(guards[index].Position)
		c.guardsByID[guards[index].ObjectID] = guards[index]
	}
	result := &MapData{Spec: spec, Terrain: terrain, Resurrection: areas, Guards: guards}
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
