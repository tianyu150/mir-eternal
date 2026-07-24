package gameworld

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func makeTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"GameMap/Maps", "GameMap/Terrains", "GameMap/MapAreas"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mapJSON := `{"MapId":142,"MapName":"Test","TerrainFile":"0142-Test","LimitPlayers":10}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/Maps/142-Test.txt"), []byte(mapJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "GameMap/Terrains/0142-Test.terrain"))
	if err != nil {
		t.Fatal(err)
	}
	header := []int32{0, 0, 10, 10, 0, 0}
	if err := binary.Write(file, binary.LittleEndian, header); err != nil {
		t.Fatal(err)
	}
	cells := make([]uint32, 100)
	for i := range cells {
		cells[i] = passableMask | 40
	}
	cells[5*10+5] = 0
	if err := binary.Write(file, binary.LittleEndian, cells); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	areaJSON := `{"FromMapId":142,"FromCoords":"1, 1","AreaType":"复活区域","RangeCoords":["1, 1","1, 2"]}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/MapAreas/142-Spawn.txt"), []byte(areaJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := OpenCatalog(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestCatalogLoadsLegacyTerrain(t *testing.T) {
	catalog := makeTestCatalog(t)
	data, err := catalog.Load(142)
	if err != nil {
		t.Fatal(err)
	}
	if data.Spec.MapName != "Test" || data.Terrain.Width != 10 || data.Terrain.Height != 10 {
		t.Fatalf("unexpected map data: %+v", data)
	}
	if !data.Terrain.CanPass(Point{2, 3}) || data.Terrain.CanPass(Point{5, 5}) {
		t.Fatal("terrain passability was decoded incorrectly")
	}
	if got := data.Terrain.Altitude(Point{2, 3}); got != 10 {
		t.Fatalf("altitude=%d", got)
	}
	if len(data.Resurrection) != 3 {
		t.Fatalf("spawn points=%v", data.Resurrection)
	}
}

func TestCoordinateConversions(t *testing.T) {
	point := Point{855, 459}
	x, y := ProtocolPoint(point)
	if got := GridPoint(x, y); got != point {
		t.Fatalf("round trip=%+v want %+v", got, point)
	}
	if Direction(Point{1, 1}, Point{2, 1}) != 0 || Direction(Point{1, 1}, Point{1, 2}) != 2048 {
		t.Fatal("direction conversion differs from C#")
	}
}

func TestRepositorySystemDataCompatibility(t *testing.T) {
	root := filepath.Join("..", "..", "Database", "System")
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		t.Skip("system database is not checked out")
	}
	catalog, err := OpenCatalog(root, 16_000_000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := catalog.Load(142)
	if err != nil {
		t.Fatal(err)
	}
	if data.Spec.MapID != 142 || data.Terrain.Width != 797 || data.Terrain.Height != 902 || len(data.Resurrection) == 0 {
		t.Fatalf("unexpected legacy map 142: spec=%+v terrain=%dx%d spawns=%d", data.Spec, data.Terrain.Width, data.Terrain.Height, len(data.Resurrection))
	}
}
