package gameworld

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func makeTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"GameMap/Maps", "GameMap/Terrains", "GameMap/MapAreas", "GameMap/TeleportGates", "GameMap/Guards", "Npc/Guards"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestMap(t, root, 142, 10, 0)
	writeTestMap(t, root, 143, 10, 5)
	areaJSON := `{"FromMapId":142,"FromCoords":"1, 1","AreaType":"复活区域","RangeCoords":["1, 1","1, 2"]}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/MapAreas/142-Spawn.txt"), []byte(areaJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	gateJSON := `{"TeleportGateNumber":1,"FromMapId":142,"ToMapId":143,"TeleportGateName":"Test Gate","FromCoords":"2, 2","ToCoords":"7, 7"}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/TeleportGates/142-1-Test.txt"), []byte(gateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	sameMapGateJSON := `{"TeleportGateNumber":2,"FromMapId":142,"ToMapId":142,"TeleportGateName":"Local Gate","FromCoords":"3, 3","ToCoords":"8, 8"}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/TeleportGates/142-2-Test.txt"), []byte(sameMapGateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	distantGateJSON := `{"TeleportGateNumber":3,"FromMapId":142,"ToMapId":142,"TeleportGateName":"Distant Gate","FromCoords":"0, 0","ToCoords":"1, 1"}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/TeleportGates/142-3-Test.txt"), []byte(distantGateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	guardTemplateJSON := `{"Name":"Test Guard","GuardNumber":7000,"Level":20,"Nothingness":false}`
	if err := os.WriteFile(filepath.Join(root, "Npc/Guards/7000-Test.txt"), []byte(guardTemplateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	// The trailing comma is intentional: four legacy placement files in the
	// repository use this Newtonsoft-compatible form.
	guardPlacementJSON := `{"GuardNumber":7000,"FromMapId":142,"FromCoords":"6, 2","Direction":"右下",}`
	if err := os.WriteFile(filepath.Join(root, "GameMap/Guards/142-Test.txt"), []byte(guardPlacementJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := OpenCatalog(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func writeTestMap(t *testing.T, root string, mapID, limit int, minLevel byte) {
	t.Helper()
	mapJSON := fmt.Sprintf(`{"MapId":%d,"MapName":"Test %d","TerrainFile":"%04d-Test","LimitPlayers":%d,"MinLevel":%d}`, mapID, mapID, mapID, limit, minLevel)
	if err := os.WriteFile(filepath.Join(root, "GameMap/Maps", fmt.Sprintf("%d-Test.txt", mapID)), []byte(mapJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "GameMap/Terrains", fmt.Sprintf("%04d-Test.terrain", mapID)))
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
}

func TestCatalogLoadsLegacyTerrain(t *testing.T) {
	catalog := makeTestCatalog(t)
	data, err := catalog.Load(142)
	if err != nil {
		t.Fatal(err)
	}
	if data.Spec.MapName != "Test 142" || data.Terrain.Width != 10 || data.Terrain.Height != 10 {
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

func TestCatalogLoadsLegacyGuard(t *testing.T) {
	catalog := makeTestCatalog(t)
	data, err := catalog.Load(142)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Guards) != 1 {
		t.Fatalf("guards=%+v", data.Guards)
	}
	guard := data.Guards[0]
	if guard.ObjectID != staticGuardObjectBase+1 || guard.Template != 7000 || guard.Name != "Test Guard" || guard.Level != 20 || guard.Position != (Point{6, 2}) || guard.Direction != 5120 || guard.MaxHP != 9999 || !guard.Blocking || guard.Altitude != 10 {
		t.Fatalf("unexpected guard: %+v", guard)
	}
}

func TestCatalogLoadsTeleportGate(t *testing.T) {
	catalog := makeTestCatalog(t)
	gate, ok := catalog.Gate(142, 1)
	if !ok {
		t.Fatal("teleport gate was not loaded")
	}
	if gate.Name != "Test Gate" || gate.From != (Point{2, 2}) || gate.To != (Point{7, 7}) || gate.ToMapID != 143 {
		t.Fatalf("unexpected gate: %+v", gate)
	}
	if _, ok := catalog.Gate(142, 99); ok {
		t.Fatal("undefined teleport gate was returned")
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
	if data.Spec.MapID != 142 || data.Terrain.Width != 797 || data.Terrain.Height != 902 || len(data.Resurrection) == 0 || len(data.Guards) != 25 {
		t.Fatalf("unexpected legacy map 142: spec=%+v terrain=%dx%d spawns=%d guards=%d", data.Spec, data.Terrain.Width, data.Terrain.Height, len(data.Resurrection), len(data.Guards))
	}
	foundGuard := false
	for _, guard := range data.Guards {
		if guard.Template == 6734 && guard.Position == (Point{935, 375}) && guard.Direction == 7168 {
			foundGuard = true
			break
		}
	}
	if !foundGuard {
		t.Fatal("legacy greatsword guard was not loaded")
	}
	gate, ok := catalog.Gate(142, 1)
	if !ok || gate.From != (Point{584, 719}) || gate.To != (Point{829, 454}) {
		t.Fatalf("unexpected legacy teleport gate: %+v, found=%t", gate, ok)
	}
}
