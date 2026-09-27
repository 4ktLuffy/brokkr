package verify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Self-verification for tasks with no hidden tests (live mode): the agent must
// write a regression test, and Brokkr checks it independently in fresh
// microVMs:
//
//	A: the original code plus only the agent's new test files
//	B: the original code plus the agent's whole patch
//
// PASS needs at least one new test that does not pass in A and passes in B,
// every new test passing in B, and no existing test that passed in A failing
// in B. A test that already passes on the unfixed code proves nothing, so it
// cannot be the evidence.

// SelfResult is the outcome of SelfTest, also written as selftest.json.
type SelfResult struct {
	Verdict     Verdict  `json:"verdict"`
	Reasons     []string `json:"reasons"`
	NewFiles    []string `json:"new_test_files"`
	NewTests    []string `json:"new_tests"`
	FailBefore  []string `json:"new_tests_failing_before"`
	PassAfter   []string `json:"new_tests_passing_after"`
	Regressions []string `json:"regressions"`
	RunA        string   `json:"run_a"`
	RunB        string   `json:"run_b"`
}

type fileDiff struct {
	path string
	text string
}

// splitPatch cuts a unified diff (diff -ruN or git style) into per-file parts.
func splitPatch(p string) []fileDiff {
	var out []fileDiff
	var cur *fileDiff
	for _, line := range strings.SplitAfter(p, "\n") {
		if strings.HasPrefix(line, "diff ") {
			out = append(out, fileDiff{})
			cur = &out[len(out)-1]
		}
		if cur == nil {
			continue
		}
		cur.text += line
		if strings.HasPrefix(line, "+++ ") && cur.path == "" {
			f := strings.Fields(strings.TrimPrefix(line, "+++ "))
			if len(f) > 0 {
				cur.path = strings.TrimPrefix(f[0], "b/")
			}
		}
	}
	return out
}

// IsNewTestFile reports whether rel is an agent-written regression test:
// under dir, named test_brokkr_*.py, and absent from the original repository.
func IsNewTestFile(dir, rel, repo string) bool {
	if dir == "" || !strings.HasPrefix(rel, dir) {
		return false
	}
	base := filepath.Base(rel)
	if !strings.HasPrefix(base, "test_brokkr_") || !strings.HasSuffix(base, ".py") {
		return false
	}
	_, err := os.Stat(filepath.Join(repo, rel))
	return os.IsNotExist(err)
}

// SplitNewTests returns the part of patch that adds new regression test files,
// and the rest.
func SplitNewTests(patch, dir, repo string) (tests, rest string, files []string) {
	for _, fd := range splitPatch(patch) {
		if IsNewTestFile(dir, fd.path, repo) {
			tests += fd.text
			files = append(files, fd.path)
		} else {
			rest += fd.text
		}
	}
	return tests, rest, files
}

// SelfTest runs the two sandboxed runs and decides. task.TestCmd selects the
// existing tests used as the regression set; the new test files are appended.
func SelfTest(cfg Config, task Task, repoDir, patchPath, outDir string) (*SelfResult, error) {
	res := &SelfResult{Reasons: []string{}, NewFiles: []string{}, NewTests: []string{}, FailBefore: []string{}, PassAfter: []string{}, Regressions: []string{}}
	write := func() (*SelfResult, error) {
		b, _ := json.MarshalIndent(res, "", "  ")
		return res, os.WriteFile(filepath.Join(outDir, "selftest.json"), append(b, '\n'), 0o644)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(patchPath)
	if err != nil {
		return nil, err
	}
	testPart, _, files := SplitNewTests(string(raw), task.NewTestsDir, repoDir)
	res.NewFiles = files
	// The runs below cannot use Protect (the new test files sit under a
	// protected directory), so the rest of the patch is checked here: it may
	// not touch a protected path at all.
	isNewFile := map[string]bool{}
	for _, f := range files {
		isNewFile[f] = true
	}
	for _, fd := range splitPatch(string(raw)) {
		if isNewFile[fd.path] {
			continue
		}
		for _, pre := range task.Protect {
			if fd.path == strings.TrimSuffix(pre, "/") || strings.HasPrefix(fd.path, pre) {
				res.Verdict = Rejected
				res.Reasons = append(res.Reasons, "patch changes protected path "+fd.path+" (only new "+task.NewTestsDir+"test_brokkr_*.py files may be added there)")
				return write()
			}
		}
	}
	if len(files) == 0 {
		res.Verdict = Fail
		res.Reasons = append(res.Reasons, fmt.Sprintf("no regression test: no new %stest_brokkr_*.py file in the patch", task.NewTestsDir))
		return write()
	}
	testOnly := filepath.Join(outDir, "tests-only.patch")
	if err := os.WriteFile(testOnly, []byte(testPart), 0o644); err != nil {
		return nil, err
	}

	t := task
	t.TestPatch, t.RequiredTests, t.Protect = "", nil, nil
	t.TestCmd = strings.TrimSpace(task.TestCmd + " " + strings.Join(files, " "))
	cc := cfg
	cc.CollectOnly = true
	a, err := Run(cc, t, repoDir, testOnly, filepath.Join(outDir, "A"))
	if err != nil {
		return nil, fmt.Errorf("self-test run A: %w", err)
	}
	b, err := Run(cc, t, repoDir, patchPath, filepath.Join(outDir, "B"))
	if err != nil {
		return nil, fmt.Errorf("self-test run B: %w", err)
	}
	res.RunA, res.RunB = a.RunID, b.RunID
	if a.Verdict != Collected || b.Verdict != Collected {
		res.Verdict = Error
		res.Reasons = append(res.Reasons, fmt.Sprintf("self-test runs did not complete: A %s %v, B %s %v", a.Verdict, a.Reasons, b.Verdict, b.Reasons))
		return write()
	}

	// "No regressions" means nothing if no existing test ran (a collection
	// error stops pytest before any test): that is the task's fault, not the
	// agent's, so it is an error, not a verdict.
	existing := 0
	for _, id := range a.Tests.Passed {
		if !isNewTest(files, id) {
			existing++
		}
	}
	if existing == 0 {
		res.Verdict = Error
		res.Reasons = append(res.Reasons, "no existing test passed in run A: the regression set did not run (check test_cmd)")
		return write()
	}
	judgeSelf(res, files, a.Tests.Passed, a.Tests.Failed, b.Tests.Passed, b.Tests.Failed)
	return write()
}

// judgeSelf fills res from the per-test statuses of run A (original code plus
// the new tests) and run B (the whole patch).
func judgeSelf(res *SelfResult, files, aPassed, aFailed, bPassed, bFailed []string) {
	isNew := func(id string) bool { return isNewTest(files, id) }
	passA, passB := set(aPassed), set(bPassed)
	all := map[string]bool{}
	for _, xs := range [][]string{aPassed, aFailed, bPassed, bFailed} {
		for _, id := range xs {
			if isNew(id) {
				all[id] = true
			}
		}
	}
	for id := range all {
		res.NewTests = append(res.NewTests, id)
		if passB[id] {
			res.PassAfter = append(res.PassAfter, id)
			if !passA[id] {
				res.FailBefore = append(res.FailBefore, id)
			}
		}
	}
	for id := range passA {
		if !isNew(id) && !passB[id] {
			res.Regressions = append(res.Regressions, id)
		}
	}
	sort.Strings(res.NewTests)
	sort.Strings(res.FailBefore)
	sort.Strings(res.PassAfter)
	sort.Strings(res.Regressions)

	switch {
	case len(res.NewTests) == 0:
		res.Reasons = append(res.Reasons, "the new test files ran no tests")
	case len(res.PassAfter) < len(res.NewTests):
		res.Reasons = append(res.Reasons, fmt.Sprintf("%d of %d new tests do not pass with the fix", len(res.NewTests)-len(res.PassAfter), len(res.NewTests)))
	case len(res.FailBefore) == 0:
		res.Reasons = append(res.Reasons, "every new test already passes without the fix: they do not detect the bug")
	}
	if len(res.Regressions) > 0 {
		res.Reasons = append(res.Reasons, fmt.Sprintf("%d existing tests pass before the fix and not after", len(res.Regressions)))
	}
	if len(res.Reasons) > 0 {
		res.Verdict = Fail
		return
	}
	res.Verdict = Pass
	res.Reasons = append(res.Reasons, fmt.Sprintf("%d new test(s) fail before the fix and pass after; no regressions among %d existing test ids", len(res.FailBefore), len(passA)-countNew(passA, isNew)))
}

func isNewTest(files []string, id string) bool {
	for _, f := range files {
		if strings.HasPrefix(id, f+"::") {
			return true
		}
	}
	return false
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func countNew(m map[string]bool, isNew func(string) bool) int {
	n := 0
	for id := range m {
		if isNew(id) {
			n++
		}
	}
	return n
}
