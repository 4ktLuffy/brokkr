// Package verify decides whether a patch fixes a task, using a Firecracker
// sandbox to run the tests and an evidence file to record why.
//
// The verdict is PASS only when all of these hold:
//   - the patch leaves every protected path (the tests) byte-for-byte unchanged,
//   - the test command exits 0 inside the sandbox, and
//   - every required test is reported as passing.
//
// The last rule catches code that exits 0 before any test runs.
package verify

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// VerifierVersion identifies the verification rules and parsers; it is recorded
// in every evidence file. 0.1 had unittest and django parsers; 0.2 adds sympy;
// 0.3 adds pass_rule (default unchanged); 0.4 adds pytest; 0.4.1 refuses a
// required_only verdict for a patch when there are no required tests.
const VerifierVersion = "0.4.1"

type Verdict string

const (
	Pass     Verdict = "PASS"     // the patch fixes the task
	Fail     Verdict = "FAIL"     // the patch ran and did not fix it
	Rejected Verdict = "REJECTED" // the patch was refused before running
	Error    Verdict = "ERROR"    // the sandbox failed; says nothing about the patch
	// Collected is not a verdict: the tests ran and their statuses are in the
	// evidence, for deriving required tests (Config.CollectOnly).
	Collected Verdict = "COLLECTED"
)

type Task struct {
	Name          string   `json:"name"`
	Issue         string   `json:"issue,omitempty"` // what a reporter would write; shown to the agent
	TestCmd       string   `json:"test_cmd"`
	TimeoutS      int      `json:"timeout_s"`
	Protect       []string `json:"protect"`
	RequiredTests []string `json:"required_tests"`
	// TestPatch, when set, is applied by the verifier after the candidate
	// patch: the hidden tests of SWE-bench-style tasks, which the agent never
	// sees. Relative paths are resolved against the task file's directory.
	TestPatch string `json:"test_patch,omitempty"`
	// Parser reads the test log: "unittest" (default), or "django" / "sympy" /
	// "pytest", ports of SWE-bench v4.1.0's parse_log_django / parse_log_sympy /
	// parse_log_pytest, whose
	// test names SWE-bench's FAIL_TO_PASS and PASS_TO_PASS lists use verbatim.
	Parser string `json:"parser,omitempty"`
	// EnvImage is a read-only ext4 image mounted at /opt/env in the guest,
	// holding the interpreter and dependencies. Relative to the task file.
	EnvImage string `json:"env_image,omitempty"`
	// PassRule is "exit_and_required" (default: exit 0 and every required test
	// passes) or "required_only", SWE-bench's own resolution criterion: every
	// required test passes and the exit code is ignored. SymPy needs it: old
	// releases have tests that error on Python 3.9 even with the gold patch,
	// and SWE-bench leaves them out of the required lists.
	PassRule string `json:"pass_rule,omitempty"`
	// Live marks a task judged by the agent's own regression test (SelfTest)
	// rather than, or in addition to, hidden tests. NewTestsDir is where that
	// test must be created (as a new test_brokkr_*.py file).
	Live        bool   `json:"live,omitempty"`
	NewTestsDir string `json:"new_tests_dir,omitempty"`
	MemMiB      int    `json:"mem_mib,omitempty"`
}

type Evidence struct {
	Schema    string          `json:"schema"`
	Verifier  string          `json:"verifier"`
	RunID     string          `json:"run_id"`
	StartedAt time.Time       `json:"started_at"`
	Host      string          `json:"host"`
	Task      Task            `json:"task"`
	Inputs    Inputs          `json:"inputs"`
	Verdict   Verdict         `json:"verdict"`
	Reasons   []string        `json:"reasons"`
	Tests     TestResults     `json:"tests"`
	Sandbox   json.RawMessage `json:"sandbox,omitempty"`
}

type Inputs struct {
	TestPatchSHA256   string `json:"test_patch_sha256,omitempty"`
	RepoTreeSHA256    string `json:"repo_tree_sha256"`
	PatchSHA256       string `json:"patch_sha256,omitempty"`
	PatchedTreeSHA256 string `json:"patched_tree_sha256,omitempty"`
}

type TestResults struct {
	Passed          []string `json:"passed"`
	Failed          []string `json:"failed"`
	MissingRequired []string `json:"missing_required"`
}

type Config struct {
	Runner string // path to brokkr-runner
	Host   string // label for the machine, recorded in the evidence
	// CollectOnly runs the tests and records per-test statuses but gives no
	// verdict (Collected). Used to derive required tests; never for scoring.
	CollectOnly bool
}

// Run verifies patchPath (empty for "no patch") against the repo at repoDir and
// writes evidence.json plus the sandbox logs into outDir.
func Run(cfg Config, task Task, repoDir, patchPath, outDir string) (*Evidence, error) {
	ev := &Evidence{
		Schema:    "brokkr.evidence/v0",
		Verifier:  VerifierVersion,
		RunID:     fmt.Sprintf("%s-%d", time.Now().UTC().Format("20060102T150405Z"), os.Getpid()),
		StartedAt: time.Now().UTC(),
		Host:      cfg.Host,
		Task:      task,
		Tests:     TestResults{Passed: []string{}, Failed: []string{}, MissingRequired: []string{}},
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	finish := func(v Verdict, reasons ...string) (*Evidence, error) {
		ev.Verdict = v
		ev.Reasons = append(ev.Reasons, reasons...)
		b, _ := json.MarshalIndent(ev, "", "  ")
		return ev, os.WriteFile(filepath.Join(outDir, "evidence.json"), append(b, '\n'), 0o644)
	}

	orig, err := treeHashes(repoDir)
	if err != nil {
		return nil, fmt.Errorf("hash repo: %w", err)
	}
	ev.Inputs.RepoTreeSHA256 = treeDigest(orig)

	stage := filepath.Join(outDir, "repo")
	_ = os.RemoveAll(stage)
	if out, err := exec.Command("cp", "-R", repoDir, stage).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("copy repo: %v: %s", err, out)
	}
	defer os.RemoveAll(stage)

	if patchPath != "" {
		patch, err := os.ReadFile(patchPath)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(patch)
		ev.Inputs.PatchSHA256 = hex.EncodeToString(sum[:])

		// git apply refuses absolute paths and "..", so a patch cannot write
		// outside the staged copy.
		apply := exec.Command("git", "apply", "--whitespace=nowarn", "-")
		apply.Dir = stage
		apply.Stdin = bytes.NewReader(patch)
		if out, err := apply.CombinedOutput(); err != nil {
			return finish(Rejected, "patch does not apply: "+strings.TrimSpace(string(out)))
		}

		patched, err := treeHashes(stage)
		if err != nil {
			return nil, fmt.Errorf("hash patched repo: %w", err)
		}
		ev.Inputs.PatchedTreeSHA256 = treeDigest(patched)
		// Compare file contents, not patch headers: whatever the patch looks
		// like, the protected files must come out identical.
		if touched := changedUnder(orig, patched, task.Protect); len(touched) > 0 {
			return finish(Rejected, "patch changes protected paths: "+strings.Join(touched, ", "))
		}
	}

	if task.TestPatch != "" {
		tp, err := os.ReadFile(task.TestPatch)
		if err != nil {
			return nil, fmt.Errorf("read test patch: %w", err)
		}
		sum := sha256.Sum256(tp)
		ev.Inputs.TestPatchSHA256 = hex.EncodeToString(sum[:])
		apply := exec.Command("git", "apply", "--whitespace=nowarn", "-")
		apply.Dir = stage
		apply.Stdin = bytes.NewReader(tp)
		if out, err := apply.CombinedOutput(); err != nil {
			// The candidate cannot touch protected (test) paths, so this is a
			// broken task, not a bad patch.
			return finish(Error, "hidden test patch does not apply: "+strings.TrimSpace(string(out)))
		}
	}

	timeout := task.TimeoutS
	if timeout <= 0 {
		timeout = 300
	}
	args := []string{"--repo", stage, "--cmd", task.TestCmd, "--timeout", fmt.Sprint(timeout), "--out", outDir}
	if task.EnvImage != "" {
		args = append(args, "--env-drive", task.EnvImage)
	}
	if task.MemMiB > 0 {
		args = append(args, "--mem-mib", fmt.Sprint(task.MemMiB))
	}
	runner := exec.Command(cfg.Runner, args...)
	runner.Stderr = os.Stderr
	report, runErr := runner.Output()
	ev.Sandbox = json.RawMessage(bytes.TrimSpace(report))

	var rep struct {
		Guest *struct {
			ExitCode int  `json:"exit_code"`
			TimedOut bool `json:"timed_out"`
		} `json:"guest"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(report, &rep); err != nil {
		ev.Sandbox = nil
		return finish(Error, fmt.Sprintf("runner output unreadable (%v): %v", runErr, err))
	}
	if rep.Error != nil || rep.Guest == nil {
		msg := "no guest result"
		if rep.Error != nil {
			msg = *rep.Error
		}
		return finish(Error, "sandbox failed: "+msg)
	}

	stdout, _ := os.ReadFile(filepath.Join(outDir, "stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(outDir, "stderr.log"))
	var passed, failed map[string]bool
	switch task.Parser {
	case "", "unittest":
		passed, failed = parseUnittest(append(stdout, stderr...))
	case "django":
		passed, failed = parseDjango(string(append(stdout, stderr...)))
	case "sympy":
		passed, failed = parseSympy(string(append(stdout, stderr...)))
	case "pytest":
		passed, failed = parsePytest(string(append(stdout, stderr...)))
	default:
		return finish(Error, "unknown parser "+task.Parser)
	}
	ev.Tests.Passed, ev.Tests.Failed = sorted(passed), sorted(failed)
	for _, t := range task.RequiredTests {
		if !passed[t] {
			ev.Tests.MissingRequired = append(ev.Tests.MissingRequired, t)
		}
	}

	if cfg.CollectOnly {
		return finish(Collected, fmt.Sprintf("collect-only: %d passed, %d failed (exit %d)", len(passed), len(failed), rep.Guest.ExitCode))
	}

	var reasons []string
	switch task.PassRule {
	case "", "exit_and_required", "required_only":
	default:
		return finish(Error, "unknown pass_rule "+task.PassRule)
	}
	if task.PassRule == "required_only" && len(task.RequiredTests) == 0 && patchPath != "" {
		// With no required tests and no exit-code check, nothing could fail:
		// a verdict would be meaningless. (The unpatched baseline run is still
		// allowed, to derive required tests from; see scripts/gh/derive_tests.py.)
		return finish(Error, "pass_rule required_only with no required tests: nothing to verify")
	}
	if rep.Guest.TimedOut {
		reasons = append(reasons, fmt.Sprintf("test command timed out after %ds", timeout))
	} else if rep.Guest.ExitCode != 0 && task.PassRule != "required_only" {
		reasons = append(reasons, fmt.Sprintf("test command exited %d", rep.Guest.ExitCode))
	}
	if n := len(ev.Tests.MissingRequired); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d of %d required tests did not report a pass", n, len(task.RequiredTests)))
	}
	if len(reasons) > 0 {
		return finish(Fail, reasons...)
	}
	if task.PassRule == "required_only" {
		return finish(Pass, fmt.Sprintf("all %d required tests passed (exit %d; pass_rule required_only)", len(task.RequiredTests), rep.Guest.ExitCode))
	}
	return finish(Pass, fmt.Sprintf("exit 0 and all %d required tests passed", len(task.RequiredTests)))
}

// "test_x (pkg.mod.Class) ... ok" (Python <3.11) or
// "test_x (pkg.mod.Class.test_x) ... ok" (3.11+). Results may be on a later line.
var unittestLine = regexp.MustCompile(`^(\w+) \(([\w.]+)\)(?: \.\.\. (.*))?$`)

func parseUnittest(out []byte) (passed, failed map[string]bool) {
	passed, failed = map[string]bool{}, map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := unittestLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		id := m[2]
		if !strings.HasSuffix(id, "."+m[1]) {
			id += "." + m[1]
		}
		switch status := strings.TrimSpace(m[3]); {
		case status == "ok":
			passed[id] = true
		case status == "FAIL" || status == "ERROR" || strings.HasPrefix(status, "expected failure"):
			failed[id] = true
		}
	}
	return passed, failed
}

// treeHashes maps each regular file's slash path to its sha256.
func treeHashes(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "symlink:" + target
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	return out, err
}

func treeDigest(files map[string]string) string {
	h := sha256.New()
	for _, k := range sortedKeys(files) {
		fmt.Fprintf(h, "%s\x00%s\n", k, files[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// changedUnder lists paths under any protected prefix that were added, removed
// or modified between a and b.
func changedUnder(a, b map[string]string, prefixes []string) []string {
	protected := func(p string) bool {
		for _, pre := range prefixes {
			if p == strings.TrimSuffix(pre, "/") || strings.HasPrefix(p, pre) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for p := range m {
			if seen[p] || !protected(p) {
				continue
			}
			seen[p] = true
			if a[p] != b[p] {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
