package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseUnittestBothFormats(t *testing.T) {
	out := []byte(`test_a (tests.t.C) ... ok
test_b (tests.t.C.test_b) ... ok
test_c (tests.t.C.test_c) ... FAIL
test_d (tests.t.C.test_d) ... ERROR
test_e (tests.t.C.test_e)
Docstring on its own line ... ok
test_f (tests.t.C.test_f) ... skipped 'no'
`)
	passed, failed := parseUnittest(out)
	if want := []string{"tests.t.C.test_a", "tests.t.C.test_b"}; !reflect.DeepEqual(sorted(passed), want) {
		t.Errorf("passed = %v, want %v", sorted(passed), want)
	}
	if want := []string{"tests.t.C.test_c", "tests.t.C.test_d"}; !reflect.DeepEqual(sorted(failed), want) {
		t.Errorf("failed = %v, want %v", sorted(failed), want)
	}
}

// A skipped or silent test never counts as a pass.
func TestParseUnittestSkippedIsNotPass(t *testing.T) {
	passed, _ := parseUnittest([]byte("test_f (tests.t.C.test_f) ... skipped 'no'\n"))
	if len(passed) != 0 {
		t.Errorf("skipped test counted as passed: %v", passed)
	}
}

func TestChangedUnder(t *testing.T) {
	a := map[string]string{"calc.py": "1", "tests/t.py": "2", "tests/u.py": "3", "testsuite.py": "4"}
	b := map[string]string{"calc.py": "9", "tests/t.py": "2", "tests/new.py": "5", "testsuite.py": "8"}
	got := changedUnder(a, b, []string{"tests/"})
	want := []string{"tests/new.py", "tests/u.py"} // added and removed; testsuite.py is not under tests/
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changedUnder = %v, want %v", got, want)
	}
}

// The cases SWE-bench's Django parser handles: plain lines, docstring names,
// statuses after extra output, and FAIL:/ERROR: summary lines.
func TestParseDjango(t *testing.T) {
	log := `test_a (app.tests.T) ... ok
test_b (app.tests.T)
Docstring of b ... ok
test_c (app.tests.T) ... Some output
ok
test_d (app.tests.T) ... FAIL
test_e (app.tests.T) ... ERROR
test_f (app.tests.T) ... skipped 'no db'
test_g (app.tests.T) ... System check identified no issues (0 silenced)
ok
======================================================================
FAIL: test_d (app.tests.T)
`
	passed, failed := parseDjango(log)
	for _, want := range []string{"test_a (app.tests.T)", "Docstring of b", "test_c (app.tests.T)", "test_g (app.tests.T)"} {
		if !passed[want] {
			t.Errorf("%q not passed; passed = %v", want, sorted(passed))
		}
	}
	if !failed["test_d (app.tests.T)"] || !failed["test_e (app.tests.T)"] {
		t.Errorf("failed = %v", sorted(failed))
	}
	if passed["test_f (app.tests.T)"] || passed["test_d (app.tests.T)"] {
		t.Errorf("skipped or failed test counted as passed: %v", sorted(passed))
	}
}

func TestParseSympy(t *testing.T) {
	log := `============================= test process starts ==============================
sympy/geometry/tests/test_point.py[3]
test_point ok
test_issue_11617 F
test_transform E
________________________________________________________________________________
_________ sympy/geometry/tests/test_point.py:test_issue_11617 __________
AssertionError
`
	passed, failed := parseSympy(log)
	if !passed["test_point"] || passed["test_issue_11617"] || passed["test_transform"] {
		t.Errorf("passed = %v", sorted(passed))
	}
	for _, f := range []string{"test_issue_11617", "test_transform", "sympy/geometry/tests/test_point.py:test_issue_11617"} {
		if !failed[f] {
			t.Errorf("%q not failed; failed = %v", f, sorted(failed))
		}
	}
}

// stubRunner writes a runner that reports exit code `exit` and the given
// unittest-style log, without booting anything.
func stubRunner(t *testing.T, exit int, log string) string {
	t.Helper()
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "log"), []byte(log), 0o644)
	r := filepath.Join(d, "runner.sh")
	os.WriteFile(r, []byte(fmt.Sprintf(`#!/bin/sh
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
mkdir -p "$out"; cp %q "$out/stderr.log"; : > "$out/stdout.log"
echo '{"guest":{"exit_code":%d,"timed_out":false,"run_ms":1},"error":null}'
`, filepath.Join(d, "log"), exit)), 0o755)
	return r
}

func TestPassRule(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "a.py"), []byte("x\n"), 0o644)
	both := "test_a (m.C.test_a) ... ok\ntest_b (m.C.test_b) ... ok\n"
	onlyA := "test_a (m.C.test_a) ... ok\n"
	cases := []struct {
		rule, log string
		exit      int
		want      Verdict
	}{
		{"required_only", both, 1, Pass},  // SWE-bench's criterion: exit code ignored
		{"required_only", onlyA, 1, Fail}, // a required test missing still fails
		{"required_only", "", 0, Fail},    // early exit: nothing reported
		{"", both, 1, Fail},               // default rule unchanged: exit 0 required
		{"exit_and_required", both, 0, Pass},
		{"bogus", both, 0, Error},
	}
	// No required tests under required_only: a patched run is an error, never a pass.
	empty := Task{Name: "t", TestCmd: "x", PassRule: "required_only"}
	patch := filepath.Join(t.TempDir(), "p.patch")
	os.WriteFile(patch, []byte("--- a/a.py\n+++ b/a.py\n@@ -1 +1 @@\n-x\n+y\n"), 0o644)
	if ev, _ := Run(Config{Runner: stubRunner(t, 0, both)}, empty, repo, patch, t.TempDir()); ev.Verdict != Error {
		t.Errorf("required_only with no required tests: verdict %s, want ERROR", ev.Verdict)
	}
	for _, c := range cases {
		task := Task{Name: "t", TestCmd: "x", RequiredTests: []string{"m.C.test_a", "m.C.test_b"}, PassRule: c.rule}
		ev, err := Run(Config{Runner: stubRunner(t, c.exit, c.log)}, task, repo, "", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if ev.Verdict != c.want {
			t.Errorf("rule=%q exit=%d log=%q: verdict %s, want %s (%v)", c.rule, c.exit, c.log, ev.Verdict, c.want, ev.Reasons)
		}
	}
}

func TestParsePytest(t *testing.T) {
	log := `tests/test_a.py::test_ok PASSED                               [ 25%]
=========================== short test summary info ============================
PASSED tests/test_a.py::test_ok
PASSED tests/test_a.py::test_param[1-x]
XFAIL tests/test_a.py::test_known - reason
SKIPPED [1] tests/test_a.py:9: no db
FAILED tests/test_a.py::test_bad - AssertionError: 1 != 2
ERROR tests/test_a.py::test_fixture - RuntimeError
`
	passed, failed := parsePytest(log)
	for _, p := range []string{"tests/test_a.py::test_ok", "tests/test_a.py::test_param[1-x]", "tests/test_a.py::test_known"} {
		if !passed[p] {
			t.Errorf("%q not passed: %v", p, sorted(passed))
		}
	}
	if !failed["tests/test_a.py::test_bad"] || !failed["tests/test_a.py::test_fixture"] || len(failed) != 2 {
		t.Errorf("failed = %v", sorted(failed))
	}
}

// Collect-only records statuses and never produces PASS/FAIL, even when no
// required tests exist.
func TestCollectOnly(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "a.py"), []byte("x\n"), 0o644)
	task := Task{Name: "t", TestCmd: "x", PassRule: "required_only"}
	ev, err := Run(Config{Runner: stubRunner(t, 1, "test_a (m.C.test_a) ... ok\n"), CollectOnly: true}, task, repo, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ev.Verdict != Collected || len(ev.Tests.Passed) != 1 {
		t.Errorf("verdict %s passed %v", ev.Verdict, ev.Tests.Passed)
	}
}
