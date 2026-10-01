package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleShowsMutationScoreOnlyWhenPresent(t *testing.T) {
	d := writeLiveRun(t, "PASS")
	res, err := Write(d, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(res.BodyPath); strings.Contains(string(body), "Mutation score") {
		t.Fatal("score shown without mutate.json")
	}
	os.WriteFile(filepath.Join(d, "mutate.json"), []byte(`{"killed":7,"survived":2}`), 0o644)
	res, err = Write(d, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(res.BodyPath); !strings.Contains(string(body), "7 of 9 broken versions") {
		t.Fatalf("score missing:\n%s", body)
	}
}
