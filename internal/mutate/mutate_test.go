package mutate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const origMod = "def clamp(x, lo, hi):\n    return x\n"

const fixedMod = `def clamp(x, lo, hi):
    if x < lo:
        return lo
    if x > hi:
        return hi
    return x
`

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"git", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
}

func writeRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "pkg"), 0o755)
	os.MkdirAll(filepath.Join(d, "tests"), 0o755)
	os.WriteFile(filepath.Join(d, "pkg/mod.py"), []byte(origMod), 0o644)
	return d
}

// patchFor builds a patch: the clamp fix plus a new test file with body.
func patchFor(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var add strings.Builder
	for _, l := range lines {
		add.WriteString("+" + l + "\n")
	}
	return `diff --git a/pkg/mod.py b/pkg/mod.py
--- a/pkg/mod.py
+++ b/pkg/mod.py
@@ -1,2 +1,6 @@
 def clamp(x, lo, hi):
+    if x < lo:
+        return lo
+    if x > hi:
+        return hi
     return x
diff --git a/tests/test_brokkr_clamp.py b/tests/test_brokkr_clamp.py
new file mode 100644
--- /dev/null
+++ b/tests/test_brokkr_clamp.py
@@ -0,0 +1,` + fmt.Sprint(len(lines)) + ` @@
` + add.String()
}

const driver = `
import importlib.util, sys, traceback
sys.path.insert(0, ".")
path = sys.argv[1]
spec = importlib.util.spec_from_file_location("t", path)
try:
    m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
except BaseException:
    sys.exit(0)  # collection error: nothing ran
for n in sorted(dir(m)):
    if n.startswith("test_"):
        try:
            getattr(m, n)(); print("PASSED %s::%s" % (path, n))
        except BaseException:
            print("FAILED %s::%s" % (path, n))
`

// oracle is a fake sandbox that really runs: it applies the patch to a copy of
// the repository and runs the new test file's test_ functions with python3.
func oracle(t *testing.T, repo string) Runner {
	return RunnerFunc(func(tag, patch string) (*Outcome, error) {
		d := t.TempDir()
		if out, err := exec.Command("cp", "-R", repo+"/.", d).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("%v %s", err, out)
		}
		ap := exec.Command("git", "apply", "-")
		ap.Dir, ap.Stdin = d, strings.NewReader(patch)
		if out, err := ap.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("apply: %s", out)
		}
		c := exec.Command("python3", "-c", driver, "tests/test_brokkr_clamp.py")
		c.Dir = d
		out, _ := c.Output()
		o := &Outcome{}
		for _, l := range strings.Split(string(out), "\n") {
			switch {
			case strings.HasPrefix(l, "PASSED "):
				o.Passed = append(o.Passed, strings.TrimPrefix(l, "PASSED "))
			case strings.HasPrefix(l, "FAILED "):
				o.Failed = append(o.Failed, strings.TrimPrefix(l, "FAILED "))
			}
		}
		return o, nil
	})
}

const strongTest = `def test_low():
    from pkg.mod import clamp
    assert clamp(-5, 0, 10) == 0

def test_high():
    from pkg.mod import clamp
    assert clamp(20, 0, 10) == 10

def test_inside():
    from pkg.mod import clamp
    assert clamp(5, 0, 10) == 5
`

// Negative control: a test that checks nothing kills nothing.
const weakTest = `def test_runs():
    from pkg.mod import clamp
    clamp(5, 0, 10)
`

// Only the lower bound is checked: the upper-bound mutants must survive.
const halfTest = `def test_low():
    from pkg.mod import clamp
    assert clamp(-5, 0, 10) == 0
`

func score(t *testing.T, body string, max int) *Result {
	t.Helper()
	needTools(t)
	repo := writeRepo(t)
	res, err := Test(oracle(t, repo), "tests/", repo, patchFor(body), Options{MaxMutants: max})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func survivorDescs(r *Result) string {
	var s []string
	for _, m := range r.Mutants {
		if m.Status == Survived {
			s = append(s, m.Desc)
		}
	}
	return strings.Join(s, " | ")
}

func TestStrongTestKillsEverythingKillable(t *testing.T) {
	r := score(t, strongTest, 0)
	if r.Generated < 8 || r.Run != r.Generated {
		t.Fatalf("generated %d run %d", r.Generated, r.Run)
	}
	if r.Score == nil || r.Survived != 0 || r.Errored != 0 {
		t.Fatalf("strong test: %s; survivors: %s", r.Line(), survivorDescs(r))
	}
}

func TestWeakTestKillsNothing(t *testing.T) {
	r := score(t, weakTest, 0)
	if r.Score == nil || r.Killed != 0 || r.Survived != r.Run {
		t.Fatalf("a test that asserts nothing killed something: %s", r.Line())
	}
	// Even putting the whole fix back goes unnoticed.
	if !strings.Contains(survivorDescs(r), "revert") {
		t.Fatalf("revert did not survive a test that checks nothing: %s", survivorDescs(r))
	}
}

func TestPartialTestShowsWhatItMisses(t *testing.T) {
	r := score(t, halfTest, 0)
	if r.Score == nil || *r.Score <= 0 || *r.Score >= 1 {
		t.Fatalf("partial test should score strictly between 0 and 1: %s", r.Line())
	}
	if !strings.Contains(survivorDescs(r), "x > hi") {
		t.Fatalf("survivors should name the upper bound: %s", survivorDescs(r))
	}
	for _, m := range r.Mutants {
		if m.Status == Survived && m.Diff == "" && !strings.Contains(m.Desc, "revert") {
			t.Errorf("survivor %s has no diff", m.ID)
		}
	}
}

func TestSampleIsBoundedAndDiverse(t *testing.T) {
	r := score(t, strongTest, 5)
	if r.Run != 5 || r.Generated <= 5 || len(r.Mutants) != 5 {
		t.Fatalf("generated %d run %d", r.Generated, r.Run)
	}
	kinds := map[string]bool{}
	for _, m := range r.Mutants {
		kinds[m.Kind] = true
	}
	if len(kinds) < 4 {
		t.Fatalf("5 mutants from %d kinds only: %v", len(kinds), kinds)
	}
}

// Negative control: the baseline must pass, or every mutant would count as
// killed by a test that simply fails.
func TestFailingBaselineRefused(t *testing.T) {
	needTools(t)
	repo := writeRepo(t)
	_, err := Test(oracle(t, repo), "tests/", repo, patchFor("def test_bad():\n    assert 0\n"), Options{})
	if err == nil || !strings.Contains(err.Error(), "no new test passes") {
		t.Fatalf("err = %v", err)
	}
}

// A run that errors is not counted either way.
func TestErroredRunNotScored(t *testing.T) {
	needTools(t)
	repo := writeRepo(t)
	inner := oracle(t, repo)
	n := 0
	r := RunnerFunc(func(tag, patch string) (*Outcome, error) {
		n++
		if n == 3 {
			return nil, fmt.Errorf("sandbox down")
		}
		return inner.Run(tag, patch)
	})
	res, err := Test(r, "tests/", repo, patchFor(strongTest), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Errored != 1 || res.Killed+res.Survived != res.Run-1 {
		t.Fatalf("errored %d killed %d survived %d run %d", res.Errored, res.Killed, res.Survived, res.Run)
	}
}

func TestNoNewTestFile(t *testing.T) {
	needTools(t)
	repo := writeRepo(t)
	fixOnly := strings.SplitN(patchFor(strongTest), "diff --git a/tests", 2)[0]
	if _, err := Test(oracle(t, repo), "tests/", repo, fixOnly, Options{}); err == nil {
		t.Fatal("a patch without a test was accepted")
	}
}

// Mutants that equal the fixed file are skipped: here the fix already returns
// None and sets a string to ”, so the "return None" and "string -> ”"
// mutants would change nothing.
func TestEquivalentMutantsSkipped(t *testing.T) {
	needTools(t)
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "pkg"), 0o755)
	os.WriteFile(filepath.Join(repo, "pkg/m.py"), []byte("def f():\n    return 1\n"), 0o644)
	fix := "diff --git a/pkg/m.py b/pkg/m.py\n--- a/pkg/m.py\n+++ b/pkg/m.py\n@@ -1,2 +1,3 @@\n def f():\n-    return 1\n+    s = ''\n+    return None\n"
	ms, _, err := Generate(repo, fix, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Kind == "return" || m.Kind == "string" {
			t.Errorf("equivalent mutant kept: %s", m.Desc)
		}
		if strings.Contains(m.Diff, "+    return None\n") && m.Kind != "revert" && strings.Count(m.Diff, "return None") > 1 {
			t.Errorf("mutant equals the fixed file: %s", m.Desc)
		}
	}
	if len(ms) == 0 {
		t.Fatal("expected some mutants (revert, deletes)")
	}
	// No mutant text may equal the fixed text: apply each and compare.
	for _, m := range ms {
		if m.Diff == "" {
			continue
		}
		got, err := applyOne(splitOne(m.Diff), "def f():\n    return 1\n", false)
		if err != nil {
			t.Fatalf("mutant %s does not apply: %v", m.Desc, err)
		}
		if got == "def f():\n    s = ''\n    return None\n" {
			t.Errorf("mutant %s equals the fixed file", m.Desc)
		}
	}
}

func TestGenerateSkipsNonPythonAndTests(t *testing.T) {
	needTools(t)
	repo := writeRepo(t)
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("a\n"), 0o644)
	fix := "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-a\n+b\n"
	ms, skipped, err := Generate(repo, fix, nil)
	if err != nil || len(ms) != 0 || len(skipped) != 1 {
		t.Fatalf("mutants %d skipped %v err %v", len(ms), skipped, err)
	}
	ms, _, _ = Generate(repo, strings.SplitN(patchFor(strongTest), "diff --git a/tests", 2)[0], []string{"pkg/"})
	if len(ms) != 0 {
		t.Fatalf("protected path mutated: %d", len(ms))
	}
}

func TestAssembleReplacesOnlyTheMutatedFile(t *testing.T) {
	needTools(t)
	repo := writeRepo(t)
	full := patchFor(strongTest)
	ms, _, err := Generate(repo, strings.SplitN(full, "diff --git a/tests", 2)[0], nil)
	if err != nil || len(ms) == 0 {
		t.Fatal(err, len(ms))
	}
	p := Assemble("TESTS\n", splitFix(full), ms[len(ms)-1])
	if !strings.HasPrefix(p, "TESTS\n") || strings.Count(p, "diff --git a/pkg/mod.py") != 1 {
		t.Fatalf("assembled:\n%s", p)
	}
}

func TestSampleDeterministic(t *testing.T) {
	var all []Mutant
	for i := 0; i < 30; i++ {
		all = append(all, Mutant{ID: fmt.Sprint(i), Kind: []string{"compare", "constant", "delete"}[i%3]})
	}
	a, b := Sample(all, 7), Sample(all, 7)
	if !reflect.DeepEqual(a, b) || len(a) != 7 {
		t.Fatal("not deterministic or wrong size")
	}
	if len(Sample(all, 0)) != 30 || len(Sample(all, 99)) != 30 {
		t.Fatal("0 or a large cap must keep everything")
	}
}

func TestBaseCmd(t *testing.T) {
	cases := map[string]string{
		"PYTHONPATH=$PWD python -m pytest -rA -p no:cacheprovider -p no:pretty tests/test_types.py": "PYTHONPATH=$PWD python -m pytest -rA -p no:cacheprovider -p no:pretty",
		"python -m pytest -rA tests/test_*.py":                                                      "python -m pytest -rA",
		"python -m pytest -rA":                                                                      "python -m pytest -rA",
	}
	for in, want := range cases {
		if got := BaseCmd(in); got != want {
			t.Errorf("BaseCmd(%q) = %q, want %q", in, got, want)
		}
	}
}

// Negative control: putting back a comment-only hunk changes no behaviour, so
// it is an equivalent mutant and is not made. The mutant that is made shows
// only its own change (delta), not the rest of the fix.
func TestCommentOnlyHunkRevertSkipped(t *testing.T) {
	needTools(t)
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "pkg"), 0o755)
	orig := "def f(x):\n    a = 1\n    b = 2\n    c = 3\n    d = 4\n    e = 5\n    f = 6\n    g = 7\n    return x\n"
	os.WriteFile(filepath.Join(repo, "pkg/m.py"), []byte(orig), 0o644)
	fix := "diff --git a/pkg/m.py b/pkg/m.py\n--- a/pkg/m.py\n+++ b/pkg/m.py\n" +
		"@@ -1,3 +1,4 @@\n+# only a comment\n def f(x):\n     a = 1\n     b = 2\n" +
		"@@ -6,4 +7,4 @@\n     e = 5\n     f = 6\n     g = 7\n-    return x\n+    return x + 1\n"
	ms, _, err := Generate(repo, fix, nil)
	if err != nil {
		t.Fatal(err)
	}
	reverts := 0
	for _, m := range ms {
		if m.Kind != "revert" {
			continue
		}
		reverts++
		if strings.Contains(m.Delta, "only a comment") || !strings.Contains(m.Delta, "-    return x + 1") {
			t.Errorf("wrong revert survived or delta wrong:\n%s", m.Delta)
		}
	}
	if reverts != 1 {
		t.Fatalf("%d revert mutants, want 1 (the comment hunk is equivalent)", reverts)
	}
}
