package verify

import (
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
