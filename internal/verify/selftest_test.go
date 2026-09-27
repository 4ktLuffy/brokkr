package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const livePatch = `diff -ruN a/pkg/mod.py b/pkg/mod.py
--- a/pkg/mod.py	2026-09-27 16:04:08
+++ b/pkg/mod.py	2026-09-27 16:04:08
@@ -1 +1 @@
-x = 1
+x = 2
diff -ruN a/tests/test_brokkr_x.py b/tests/test_brokkr_x.py
--- a/tests/test_brokkr_x.py	1970-01-01 03:00:00
+++ b/tests/test_brokkr_x.py	2026-09-27 16:04:08
@@ -0,0 +1,2 @@
+def test_x():
+    assert 1
diff -ruN a/tests/test_brokkr_old.py b/tests/test_brokkr_old.py
--- a/tests/test_brokkr_old.py	2026-09-27 16:04:08
+++ b/tests/test_brokkr_old.py	2026-09-27 16:04:08
@@ -1 +1 @@
-a
+b
`

func TestSplitNewTests(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "tests"), 0o755)
	// Named like a regression test but already in the repository: an edit
	// to an existing file, never a new test.
	os.WriteFile(filepath.Join(repo, "tests/test_brokkr_old.py"), []byte("a\n"), 0o644)
	tests, rest, files := SplitNewTests(livePatch, "tests/", repo)
	if !reflect.DeepEqual(files, []string{"tests/test_brokkr_x.py"}) {
		t.Fatalf("files = %v", files)
	}
	if want := "+def test_x():"; !contains(tests, want) || contains(rest, want) {
		t.Fatalf("test part wrong:\n%s", tests)
	}
	if !contains(rest, "+x = 2") || !contains(rest, "test_brokkr_old.py") {
		t.Fatalf("rest wrong:\n%s", rest)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestJudgeSelf(t *testing.T) {
	f := []string{"tests/test_brokkr_x.py"}
	n1, n2, old := "tests/test_brokkr_x.py::test_a", "tests/test_brokkr_x.py::test_b", "tests/test_m.py::test_old"
	cases := []struct {
		name           string
		aP, aF, bP, bF []string
		want           Verdict
	}{
		{"fails before, passes after", []string{old}, []string{n1}, []string{old, n1}, nil, Pass},
		// Negative controls.
		{"passes without the fix", []string{old, n1}, nil, []string{old, n1}, nil, Fail},
		{"still fails with the fix", []string{old}, []string{n1}, []string{old}, []string{n1}, Fail},
		{"one new test fails after", []string{old}, []string{n1, n2}, []string{old, n1}, []string{n2}, Fail},
		{"regression in an existing test", []string{old}, []string{n1}, []string{n1}, []string{old}, Fail},
		{"new file ran no tests", []string{old}, nil, []string{old}, nil, Fail},
	}
	for _, c := range cases {
		res := &SelfResult{}
		judgeSelf(res, f, c.aP, c.aF, c.bP, c.bF)
		if res.Verdict != c.want {
			t.Errorf("%s: verdict %s (%v), want %s", c.name, res.Verdict, res.Reasons, c.want)
		}
	}
}

func TestSelfTestRejectsProtectedEditsWithoutRunning(t *testing.T) {
	repo, out := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(repo, "tests"), 0o755)
	os.WriteFile(filepath.Join(repo, "tests/test_brokkr_old.py"), []byte("a\n"), 0o644)
	pp := filepath.Join(out, "p.diff")
	os.WriteFile(pp, []byte(livePatch), 0o644)
	// Runner does not exist: reaching a sandbox run would be an error.
	res, err := SelfTest(Config{Runner: "/nonexistent"}, Task{Protect: []string{"tests/"}, NewTestsDir: "tests/", Live: true}, repo, pp, out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Rejected {
		t.Fatalf("verdict %s %v, want REJECTED", res.Verdict, res.Reasons)
	}
}

func TestSelfTestFailsWithoutARegressionTest(t *testing.T) {
	repo, out := t.TempDir(), t.TempDir()
	pp := filepath.Join(out, "p.diff")
	os.WriteFile(pp, []byte("diff -ruN a/m.py b/m.py\n--- a/m.py\n+++ b/m.py\n@@ -1 +1 @@\n-x\n+y\n"), 0o644)
	res, err := SelfTest(Config{Runner: "/nonexistent"}, Task{NewTestsDir: "tests/", Live: true}, repo, pp, out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Fail {
		t.Fatalf("verdict %s, want FAIL", res.Verdict)
	}
}
