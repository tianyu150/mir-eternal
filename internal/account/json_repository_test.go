package account

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONRepositoryLegacyCompatibility(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "Account": "Legacy1",
  "PasswordEncrypted": false,
  "Password": "secret",
  "Question": "pet",
  "Answer": "cat",
  "CreatedDate": "0001-01-01T00:00:00"
}`
	if err := os.WriteFile(filepath.Join(dir, "Legacy1.txt"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenJSONRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := repo.Get(context.Background(), "LEGACY1")
	if err != nil {
		t.Fatal(err)
	}
	if acc.Name != "Legacy1" || !acc.CreatedAt.IsZero() {
		t.Fatalf("unexpected account: %+v", acc)
	}
	if err := repo.UpdatePassword(context.Background(), "legacy1", "hash", true); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenJSONRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc, err = reopened.Get(context.Background(), "Legacy1")
	if err != nil || acc.Password != "hash" || !acc.PasswordEncrypted {
		t.Fatalf("unexpected reopened account: %+v, %v", acc, err)
	}
}
