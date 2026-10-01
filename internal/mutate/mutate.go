// Package mutate tests the agent's regression test. Live mode proves the test
// fails before the fix and passes after it; that does not show the test pins
// the fix down. Mutation testing does: break the fix in small ways, run only
// the agent's new tests against each broken version, and count how many of
// the broken versions the tests notice (kill).
//
// Mutants are made from the fix part of the patch only (the patch minus the
// new test_brokkr_* files, verify.SplitNewTests), by mutator.py, which parses
// Python and never runs it. A mutant is "killed" when a new test that passed
// with the real fix does not pass with it. The mutants that survive are the
// useful output: each is a change to the fix the test cannot see.
package mutate

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4ktLuffy/brokkr/internal/verify"
)

//go:embed mutator.py
var mutatorPy string

const (
	Killed   = "killed"
	Survived = "survived"
	Errored  = "error" // the run itself failed: says nothing about the test
)

// Mutant is one broken version of the fix.
type Mutant struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Desc   string `json:"desc"`
	Diff   string `json:"diff"`  // original file -> mutated file: what the sandbox applies
	Delta  string `json:"delta"` // fixed file -> mutated file: what the mutation changed
	Status string `json:"status,omitempty"`
	// KilledBy lists the new tests that passed with the real fix and do not
	// pass with this mutant.
	KilledBy []string `json:"killed_by,omitempty"`
	// Crash marks a kill where no new test ran at all (an import or collection
	// error): a mutant that does not even load is caught by anything.
	Crash bool   `json:"crash,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Outcome is what one sandbox run says about the new tests.
type Outcome struct {
	Passed []string
	Failed []string
}

// Runner runs the agent's new tests against a whole patch (the new tests plus
// a fix or a mutant of it) on the original code.
type Runner interface {
	Run(tag, patch string) (*Outcome, error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(tag, patch string) (*Outcome, error)

func (f RunnerFunc) Run(tag, patch string) (*Outcome, error) { return f(tag, patch) }

// Result is written as mutate.json.
type Result struct {
	NewFiles      []string `json:"new_test_files"`
	TestCmd       string   `json:"test_cmd,omitempty"`
	BaselineTests []string `json:"baseline_tests_passing"` // new tests passing with the real fix
	Generated     int      `json:"generated"`              // distinct mutants made
	Run           int      `json:"run"`                    // sampled and run
	Killed        int      `json:"killed"`
	Crashed       int      `json:"killed_by_crash"` // part of Killed
	Survived      int      `json:"survived"`
	Errored       int      `json:"errored"` // not counted in the score
	Score         *float64 `json:"score"`   // Killed / (Killed + Survived); nil when nothing ran
	Skipped       []string `json:"skipped,omitempty"`
	Mutants       []Mutant `json:"mutants"`
}

// Options bound a run.
type Options struct {
	MaxMutants int // 0: all
	// Protect lists path prefixes (the tests) that are never mutated.
	Protect []string
	// OnMutant, if set, is called after each mutant has been judged.
	OnMutant func(done, total int, m *Mutant)
}

// Test runs the whole procedure. repoDir is the original repository, patch the
// agent's full patch (fix and new tests); new tests live under newTestsDir.
func Test(r Runner, newTestsDir, repoDir, patch string, opt Options) (*Result, error) {
	testPart, rest, files := verify.SplitNewTests(patch, newTestsDir, repoDir)
	return run(r, repoDir, patch, testPart, rest, files, opt)
}

func run(r Runner, repoDir, patch, testPart, rest string, files []string, opt Options) (*Result, error) {
	res := &Result{NewFiles: files, BaselineTests: []string{}, Mutants: []Mutant{}}
	if len(files) == 0 {
		return nil, fmt.Errorf("no new test file in the patch: nothing to test")
	}
	all, skipped, err := Generate(repoDir, rest, opt.Protect)
	if err != nil {
		return nil, err
	}
	res.Skipped = skipped
	res.Generated = len(all)

	// The score only means something if the new tests pass with the real fix:
	// a test that fails there would "kill" every mutant.
	base, err := r.Run("baseline", patch)
	if err != nil {
		return nil, fmt.Errorf("baseline run (real fix + new tests): %w", err)
	}
	res.BaselineTests = append(res.BaselineTests, base.Passed...)
	sort.Strings(res.BaselineTests)
	if len(base.Passed) == 0 {
		return res, fmt.Errorf("no new test passes with the real fix (failed: %v): mutation testing needs a passing test", base.Failed)
	}
	passBase := toSet(base.Passed)

	chosen := Sample(all, opt.MaxMutants)
	res.Run = len(chosen)
	fixFiles := verify.SplitPatch(rest)
	for i := range chosen {
		m := chosen[i]
		out, err := r.Run("m"+m.ID, Assemble(testPart, fixFiles, m))
		switch {
		case err != nil:
			m.Status, m.Note = Errored, err.Error()
		default:
			judge(&m, out, passBase)
		}
		switch m.Status {
		case Killed:
			res.Killed++
			if m.Crash {
				res.Crashed++
			}
		case Survived:
			res.Survived++
		default:
			res.Errored++
		}
		res.Mutants = append(res.Mutants, m)
		if opt.OnMutant != nil {
			opt.OnMutant(i+1, len(chosen), &m)
		}
	}
	if n := res.Killed + res.Survived; n > 0 {
		s := float64(res.Killed) / float64(n)
		res.Score = &s
	}
	return res, nil
}

// judge decides one mutant: killed when any test that passed with the real fix
// does not pass now.
func judge(m *Mutant, out *Outcome, passBase map[string]bool) {
	passNow := toSet(out.Passed)
	for id := range passBase {
		if !passNow[id] {
			m.KilledBy = append(m.KilledBy, id)
		}
	}
	sort.Strings(m.KilledBy)
	if len(m.KilledBy) == 0 {
		m.Status = Survived
		return
	}
	m.Status = Killed
	// Nothing ran at all: the mutant broke loading, not behaviour.
	m.Crash = len(out.Passed) == 0 && len(out.Failed) == 0
}

// Assemble builds the patch for one mutant: the new tests, plus the fix with
// the mutated file's diff in place of the fix's own diff of that file.
func Assemble(testPart string, fix []verify.FileDiff, m Mutant) string {
	var b strings.Builder
	b.WriteString(testPart)
	for _, fd := range fix {
		if fd.Path == m.Path {
			b.WriteString(m.Diff)
		} else {
			b.WriteString(fd.Text)
		}
	}
	return b.String()
}

// Generate makes the distinct, parseable mutants of the fix part of a patch.
// Files that are not Python, are under a protected prefix, or are deleted are
// left out (and the fix keeps their real diff).
func Generate(repoDir, fixPatch string, protect []string) ([]Mutant, []string, error) {
	type file struct {
		Path    string `json:"path"`
		Orig    string `json:"orig"`
		Patched string `json:"patched"`
		New     bool   `json:"new,omitempty"`
	}
	var req struct {
		Files []file `json:"files"`
	}
	var skipped []string
	for _, fd := range verify.SplitPatch(fixPatch) {
		if !strings.HasSuffix(fd.Path, ".py") || !filepath.IsLocal(fd.Path) {
			if fd.Path != "" {
				skipped = append(skipped, fd.Path+": not a Python file, left as the fix has it")
			}
			continue
		}
		if underAny(fd.Path, protect) {
			continue
		}
		f := file{Path: fd.Path}
		if raw, err := os.ReadFile(filepath.Join(repoDir, fd.Path)); err == nil {
			f.Orig = string(raw)
		} else if os.IsNotExist(err) {
			f.New = true
		} else {
			return nil, nil, err
		}
		patched, err := applyOne(fd, f.Orig, f.New)
		if err != nil {
			skipped = append(skipped, fd.Path+": "+err.Error())
			continue
		}
		f.Patched = patched
		req.Files = append(req.Files, f)
	}
	if len(req.Files) == 0 {
		return nil, skipped, nil
	}
	in, _ := json.Marshal(req)
	py := os.Getenv("BROKKR_PYTHON")
	if py == "" {
		py = "python3"
	}
	cmd := exec.Command(py, "-c", mutatorPy)
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("mutator (%s): %v: %s", py, err, strings.TrimSpace(errb.String()))
	}
	var resp struct {
		Mutants []Mutant `json:"mutants"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return nil, nil, fmt.Errorf("mutator output: %w", err)
	}
	for i := range resp.Mutants {
		resp.Mutants[i].ID = fmt.Sprintf("%02d", i+1)
	}
	return resp.Mutants, append(skipped, resp.Skipped...), nil
}

// applyOne applies one file's diff to orig in a scratch directory and returns
// the result. git is used, as everywhere in Brokkr, so the text is exactly
// what the verifier would build.
func applyOne(fd verify.FileDiff, orig string, isNew bool) (string, error) {
	dir, err := os.MkdirTemp("", "brokkr-mutate-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, fd.Path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if !isNew {
		if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
			return "", err
		}
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fd.Text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("diff does not apply alone: %s", strings.TrimSpace(string(out)))
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("file is deleted or renamed by the fix")
	}
	return string(raw), nil
}

var kindOrder = []string{"revert", "compare", "boolop", "negate", "constant", "string", "return", "delete"}

// Sample picks up to n mutants, spread over kinds (round robin) and, inside a
// kind, over the file (a bit-reversal order, so early picks are far apart).
// It is deterministic. The result keeps generation order.
func Sample(all []Mutant, n int) []Mutant {
	if n <= 0 || len(all) <= n {
		return all
	}
	buckets := map[string][]int{}
	for i, m := range all {
		buckets[m.Kind] = append(buckets[m.Kind], i)
	}
	var kinds []string
	for _, k := range kindOrder {
		if len(buckets[k]) > 0 {
			kinds = append(kinds, k)
		}
	}
	for k := range buckets {
		if !contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	order := map[string][]int{}
	for _, k := range kinds {
		b := buckets[k]
		for _, j := range bitReversal(len(b)) {
			order[k] = append(order[k], b[j])
		}
	}
	var picked []int
	for round := 0; len(picked) < n; round++ {
		progress := false
		for _, k := range kinds {
			if round < len(order[k]) && len(picked) < n {
				picked = append(picked, order[k][round])
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	sort.Ints(picked)
	out := make([]Mutant, 0, len(picked))
	for _, i := range picked {
		out = append(out, all[i])
	}
	return out
}

// bitReversal returns 0..k-1 ordered so consecutive entries are far apart.
func bitReversal(k int) []int {
	bits := 0
	for 1<<bits < k {
		bits++
	}
	var out []int
	for i := 0; i < 1<<bits; i++ {
		r := 0
		for b := 0; b < bits; b++ {
			if i>>b&1 == 1 {
				r |= 1 << (bits - 1 - b)
			}
		}
		if r < k {
			out = append(out, r)
		}
	}
	return out
}

// BaseCmd drops the trailing test paths from a task's test command, leaving
// the runner and its options: `... pytest -rA tests/test_*.py` becomes
// `... pytest -rA`. Mutation runs only the agent's new test files.
func BaseCmd(cmd string) string {
	f := strings.Fields(cmd)
	for len(f) > 0 {
		last := f[len(f)-1]
		if strings.HasPrefix(last, "-") || !(strings.Contains(last, "/") || strings.Contains(last, "*") || strings.HasSuffix(last, ".py")) {
			break
		}
		f = f[:len(f)-1]
	}
	return strings.Join(f, " ")
}

func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && (path == strings.TrimSuffix(p, "/") || strings.HasPrefix(path, p)) {
			return true
		}
	}
	return false
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
