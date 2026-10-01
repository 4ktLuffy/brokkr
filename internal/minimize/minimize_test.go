package minimize

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s%d\n", prefix, i)
	}
	return b.String()
}

func write(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a repo (dir/a) and a changed copy (dir/b), and returns the
// repo path and the git-style diff between them.
func fixture(t *testing.T, orig, changed map[string]string) (repo, patch string) {
	t.Helper()
	dir := t.TempDir()
	for k, v := range orig {
		write(t, dir, "a/"+k, v)
	}
	for k, v := range changed {
		write(t, dir, "b/"+k, v)
	}
	for k := range orig {
		if _, ok := changed[k]; !ok {
			t.Fatalf("changed lacks %s", k)
		}
	}
	c := exec.Command("git", "diff", "--no-index", "--no-color", "a", "b")
	c.Dir = dir
	out, _ := c.Output() // exit status 1 means "differs"
	if len(out) == 0 {
		t.Fatal("empty diff")
	}
	// git names the files a/a/x and b/b/x; the patch should say a/x and b/x.
	text := strings.NewReplacer("a/a/", "a/", "b/b/", "b/").Replace(string(out))
	return filepath.Join(dir, "a"), text
}

// edit replaces line n (1-based) of text.
func edit(text string, n int, with string) string {
	ls := strings.Split(text, "\n")
	ls[n-1] = with
	return strings.Join(ls, "\n")
}

// four hunks in a.py, two in b.py, a new file c.py.
func standard(t *testing.T) (string, string) {
	a, b := lines("a", 80), lines("b", 80)
	na := edit(edit(edit(edit(a, 5, "A5new"), 25, "A25new"), 45, "A45new"), 65, "A65new")
	nb := edit(edit(b, 10, "B10new"), 50, "B50new")
	return fixture(t, map[string]string{"a.py": a, "b.py": b}, map[string]string{"a.py": na, "b.py": nb, "c.py": "c1\nc2\n"})
}

// fake builds an oracle from a predicate over the patch text, recording calls
// and failing the test if it is asked about a patch that does not apply.
type fake struct {
	t     *testing.T
	repo  string
	calls []string
	pass  func(text string) bool
}

func (f *fake) oracle(text string) (string, string, error) {
	if text != "" {
		if err := GitApplies(f.repo)(text); err != nil {
			f.t.Errorf("oracle called with a patch that does not apply: %v", err)
		}
	}
	f.calls = append(f.calls, text)
	if f.pass(text) {
		return "PASS", fmt.Sprintf("run-%d", len(f.calls)), nil
	}
	return "FAIL", fmt.Sprintf("run-%d", len(f.calls)), nil
}

func needs(markers ...string) func(string) bool {
	return func(text string) bool {
		for _, m := range markers {
			if !strings.Contains(text, "+"+m+"\n") {
				return false
			}
		}
		return true
	}
}

func (f *fake) cfg(g string, max int) Config {
	return Config{Oracle: f.oracle, Applies: GitApplies(f.repo), Granularity: g, MaxRuns: max}
}

func TestOnlyTwoHunksNeeded(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: needs("A25new", "B50new")}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OneMinimal || rep.UnitsBefore != 7 || rep.UnitsAfter != 2 {
		t.Fatalf("units %d -> %d, one-minimal %v", rep.UnitsBefore, rep.UnitsAfter, rep.OneMinimal)
	}
	if !f.pass(rep.MinimalPatch) || strings.Contains(rep.MinimalPatch, "A5new") || strings.Contains(rep.MinimalPatch, "c1") {
		t.Fatalf("wrong result:\n%s", rep.MinimalPatch)
	}
	if rep.LinesBefore != 2*4+2*2+2 || rep.LinesAfter != 4 {
		t.Fatalf("lines %d -> %d", rep.LinesBefore, rep.LinesAfter)
	}
	// The kept hunks are the right ones, listed by name.
	if len(rep.Kept) != 2 || !strings.Contains(rep.Kept[0], "a.py") || !strings.Contains(rep.Kept[1], "b.py") {
		t.Fatalf("kept %v", rep.Kept)
	}
}

// A candidate that drops a hunk of a file must still apply, with the later
// hunks' line numbers fixed up: a hunk that adds lines before the kept one.
func TestRenderedSubsetsApplyAndGiveTheExpectedFile(t *testing.T) {
	a := lines("a", 80)
	na := strings.Replace(a, "a5\n", "a5\nextra1\nextra2\nextra3\n", 1)
	na = strings.Replace(na, "a40\n", "", 1)
	na = edit(na, 60, "A60new")
	repo, patch := fixture(t, map[string]string{"a.py": a}, map[string]string{"a.py": na})
	p, err := parse(patch)
	if err != nil || len(p.units) != 3 {
		t.Fatalf("parse: %v, %d units", err, len(p.units))
	}
	if got := p.render(keepSet([]int{0, 1, 2}), nil); got != patch {
		t.Fatalf("round trip changed the patch:\n%s\nvs\n%s", got, patch)
	}
	for _, keep := range [][]int{{2}, {1, 2}, {0, 2}, {1}} {
		sub := p.render(keepSet(keep), nil)
		if err := GitApplies(repo)(sub); err != nil {
			t.Fatalf("keep %v does not apply: %v\n%s", keep, err, sub)
		}
	}
	// And the result of {2} is really only the third change.
	dir := t.TempDir()
	write(t, dir, "a.py", a)
	c := exec.Command("git", "apply", "-")
	c.Dir = dir
	c.Stdin = strings.NewReader(p.render(keepSet([]int{2}), nil))
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.py"))
	if string(got) != strings.Replace(a, "a58\n", "A60new\n", 1) {
		t.Fatal("applying the subset did not give the expected file")
	}
}

func TestInteractingHunks(t *testing.T) {
	repo, patch := standard(t)
	// Either A5 and A45 together, or B10 alone, make it pass.
	pass := func(s string) bool {
		return needs("A5new", "A45new")(s) || needs("B10new")(s)
	}
	f := &fake{t: t, repo: repo, pass: pass}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OneMinimal || !pass(rep.MinimalPatch) {
		t.Fatalf("not a passing 1-minimal result:\n%s", rep.MinimalPatch)
	}
	// 1-minimality, checked from outside: every single removal fails.
	p, _ := parse(rep.MinimalPatch)
	for i := range p.units {
		var rest []int
		for j := range p.units {
			if j != i {
				rest = append(rest, j)
			}
		}
		if pass(p.render(keepSet(rest), nil)) {
			t.Fatalf("unit %d of the result can still be removed", i)
		}
	}
}

func TestNewFileIsAUnit(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: needs("c1", "c2", "A45new")}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep.UnitsAfter != 2 || !strings.Contains(rep.MinimalPatch, "new file mode") || !f.pass(rep.MinimalPatch) {
		t.Fatalf("new file not kept as a unit:\n%s", rep.MinimalPatch)
	}
	// And a new file nothing needs is removed.
	f = &fake{t: t, repo: repo, pass: needs("A45new")}
	rep, err = Minimize(patch, f.cfg("hunk", 0))
	if err != nil || strings.Contains(rep.MinimalPatch, "c.py") || rep.UnitsAfter != 1 {
		t.Fatalf("unneeded new file kept (err %v):\n%s", err, rep.MinimalPatch)
	}
}

// A second diff that edits a file the first one creates cannot apply without
// it. Such candidates are decided without calling the oracle.
func TestNonApplicableSubsetsAreNotRun(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "repo/x.py", "x\n")
	patch := `diff --git a/d.py b/d.py
new file mode 100644
--- /dev/null
+++ b/d.py
@@ -0,0 +1,3 @@
+d1
+d2
+d3
diff --git a/d.py b/d.py
--- a/d.py
+++ b/d.py
@@ -1,3 +1,3 @@
 d1
-d2
+D2new
 d3
`
	repo := filepath.Join(dir, "repo")
	f := &fake{t: t, repo: repo, pass: needs("D2new")}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep.NoApply == 0 {
		t.Fatal("expected a subset that does not apply")
	}
	if rep.UnitsAfter != 2 || !rep.OneMinimal {
		t.Fatalf("both units are needed (the edit needs the file): %d units", rep.UnitsAfter)
	}
	for _, r := range rep.Runs {
		if r.Verdict == "NOAPPLY" && r.RunID != "" {
			t.Fatal("a non-applicable subset has a run id")
		}
	}
	if rep.OracleRuns != len(f.calls) {
		t.Fatalf("runs %d but oracle called %d times", rep.OracleRuns, len(f.calls))
	}
}

func TestBudgetExhausted(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: needs("A5new")}
	rep, err := Minimize(patch, f.cfg("hunk", 3))
	if err != nil {
		t.Fatal(err)
	}
	if rep.OneMinimal || rep.Stopped == "" || rep.OracleRuns != 3 || len(f.calls) != 3 {
		t.Fatalf("one-minimal %v stopped %q runs %d calls %d", rep.OneMinimal, rep.Stopped, rep.OracleRuns, len(f.calls))
	}
	if !f.pass(rep.MinimalPatch) {
		t.Fatal("the best patch found after the budget ran out must still pass")
	}
	if rep.UnitsAfter >= rep.UnitsBefore {
		t.Fatalf("expected some reduction in 3 runs, got %d -> %d", rep.UnitsBefore, rep.UnitsAfter)
	}
	if !strings.Contains(rep.Summary(), "NOT proven 1-minimal") {
		t.Fatal("summary must say the result is not proven minimal")
	}
}

// Negative control: when every hunk is needed, nothing is removed.
func TestEveryHunkNeededStaysWhole(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: needs("A5new", "A25new", "A45new", "A65new", "B10new", "B50new", "c1")}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep.MinimalPatch != patch || rep.UnitsAfter != 7 || len(rep.Removed) != 0 || !rep.OneMinimal {
		t.Fatalf("a patch where everything is needed changed: removed %v", rep.Removed)
	}
}

func TestFullPatchMustPass(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: func(string) bool { return false }}
	_, err := Minimize(patch, f.cfg("hunk", 0))
	if err == nil || !strings.Contains(err.Error(), "does not pass") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("spent %d runs on a patch that fails", len(f.calls))
	}
}

func TestOracleErrorReturnsBestSoFar(t *testing.T) {
	repo, patch := standard(t)
	n := 0
	cfg := Config{Applies: GitApplies(repo), Oracle: func(text string) (string, string, error) {
		n++
		if n == 5 {
			return "", "", errors.New("sandbox died")
		}
		if needs("B50new")(text) {
			return "PASS", "r", nil
		}
		return "FAIL", "r", nil
	}}
	rep, err := Minimize(patch, cfg)
	if err == nil || !strings.Contains(err.Error(), "sandbox died") {
		t.Fatalf("err = %v", err)
	}
	if !needs("B50new")(rep.MinimalPatch) || rep.OneMinimal {
		t.Fatal("expected a passing, not-proven result")
	}
	if last := rep.Runs[len(rep.Runs)-1]; last.Verdict != "ERROR" {
		t.Fatalf("the failed run is not recorded: %+v", last)
	}
}

func TestPatchTheTaskPassesWithoutIsEmptied(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: func(string) bool { return true }}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil || rep.MinimalPatch != "" || rep.UnitsAfter != 0 {
		t.Fatalf("err %v, patch %q", err, rep.MinimalPatch)
	}
}

func TestCacheAvoidsRepeatedRuns(t *testing.T) {
	repo, patch := standard(t)
	f := &fake{t: t, repo: repo, pass: needs("A25new", "B50new")}
	rep, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range f.calls {
		if seen[c] {
			t.Fatal("the same candidate went to the oracle twice")
		}
		seen[c] = true
	}
	if rep.CacheHits == 0 {
		t.Fatal("expected the n=2 complements to hit the cache")
	}
}

func TestLineGranularity(t *testing.T) {
	a := lines("a", 40)
	// One hunk: a replaced line, plus two added debug lines.
	na := strings.Replace(a, "a10\n", "A10fixed\nprint('debug1')\nprint('debug2')\n", 1)
	repo, patch := fixture(t, map[string]string{"a.py": a}, map[string]string{"a.py": na})
	f := &fake{t: t, repo: repo, pass: needs("A10fixed")}

	hunk, err := Minimize(patch, f.cfg("hunk", 0))
	if err != nil || hunk.LinesAfter != hunk.LinesBefore {
		t.Fatalf("hunk mode cannot split a hunk: %v", err)
	}
	line, err := Minimize(patch, f.cfg("line", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !line.OneMinimal || strings.Contains(line.MinimalPatch, "debug") || !f.pass(line.MinimalPatch) {
		t.Fatalf("debug lines kept:\n%s", line.MinimalPatch)
	}
	// "-a10" is not needed by the oracle, so it is turned into context: the
	// old line stays beside the new one, and the patch still applies.
	if line.LinesAfter != 1 || strings.Contains(line.MinimalPatch, "-a10") {
		t.Fatalf("lines after = %d:\n%s", line.LinesAfter, line.MinimalPatch)
	}
	if err := GitApplies(repo)(line.MinimalPatch); err != nil {
		t.Fatal(err)
	}
}

func TestLineModeKeepsLinesTheOracleNeeds(t *testing.T) {
	a := lines("a", 40)
	na := strings.Replace(a, "a10\n", "A10fixed\nprint('debug1')\n", 1)
	repo, patch := fixture(t, map[string]string{"a.py": a}, map[string]string{"a.py": na})
	// Negative control: if the removed line is needed too, it stays.
	pass := func(s string) bool { return strings.Contains(s, "-a10\n") && strings.Contains(s, "+A10fixed\n") }
	f := &fake{t: t, repo: repo, pass: pass}
	rep, err := Minimize(patch, f.cfg("line", 0))
	if err != nil || rep.LinesAfter != 2 || !pass(rep.MinimalPatch) || strings.Contains(rep.MinimalPatch, "debug") {
		t.Fatalf("err %v:\n%s", err, rep.MinimalPatch)
	}
}

func TestNoNewlineMarkerSurvives(t *testing.T) {
	a := "one\ntwo\nthree"
	na := "one\nTWO\nthree\nfour"
	repo, patch := fixture(t, map[string]string{"a.txt": a}, map[string]string{"a.txt": na})
	if !strings.Contains(patch, `\ No newline`) {
		t.Fatal("fixture lost the marker")
	}
	f := &fake{t: t, repo: repo, pass: func(string) bool { return true }}
	p, err := parse(patch)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.render(keepSet([]int{0}), nil); got != patch {
		t.Fatalf("round trip lost the marker:\n%s", got)
	}
	if _, err := Minimize(patch, f.cfg("line", 0)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRefusesNonPatches(t *testing.T) {
	if _, err := Minimize("not a patch\n", Config{}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Minimize("x", Config{Granularity: "word"}); err == nil {
		t.Fatal("expected an error for a bad granularity")
	}
}

// ddmin on its own: for a monotone oracle with a known needed set, the result
// is exactly that set (it is the unique minimal one).
func TestDDMinFindsExactNeededSet(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		m := 1 + rng.Intn(40)
		need := map[int]bool{}
		for i := 0; i < m; i++ {
			if rng.Intn(4) == 0 {
				need[i] = true
			}
		}
		all := make([]int, m)
		for i := range all {
			all[i] = i
		}
		test := func(s []int) (bool, error) {
			have := keepSet(s)
			for i := range need {
				if !have[i] {
					return false, nil
				}
			}
			return true, nil
		}
		got, err := ddmin(all, test)
		if err != nil || len(got) != len(need) {
			t.Fatalf("trial %d: need %v got %v (%v)", trial, need, got, err)
		}
		for _, i := range got {
			if !need[i] {
				t.Fatalf("trial %d: kept %d which is not needed", trial, i)
			}
		}
	}
}

// Negative control for the engine: a non-monotone oracle whose only passing
// sets are the full set and one pair still yields a passing, 1-minimal set.
func TestDDMinNonMonotoneStaysPassing(t *testing.T) {
	test := func(s []int) (bool, error) {
		h := keepSet(s)
		return len(s) == 6 || (h[1] && h[4] && len(s) == 2), nil
	}
	got, err := ddmin([]int{0, 1, 2, 3, 4, 5}, test)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := test(got); !ok {
		t.Fatalf("result %v does not pass", got)
	}
}
