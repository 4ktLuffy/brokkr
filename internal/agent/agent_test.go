package agent

import (
	"context"
	"fmt"
	"github.com/4ktLuffy/brokkr/internal/route"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/4ktLuffy/brokkr/internal/model"
	"github.com/4ktLuffy/brokkr/internal/verify"
)

const service = `class Inventory:
    def release(self, sku, qty):
        item = self._items[sku]
        item.reserved -= qty

    def ship(self, sku, qty):
        item = self._items[sku]
        item.reserved -= qty
        item.on_hand -= qty
`

func newWS(t *testing.T) (*workspace, string) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "service.py")
	if err := os.WriteFile(p, []byte(service), 0o644); err != nil {
		t.Fatal(err)
	}
	return &workspace{root: root, protect: []string{"tests/"}}, p
}

// The failure seen in the first eval: the model sends a method body with
// 4-space indentation where the file has 8.
func TestReplaceReindentsBlock(t *testing.T) {
	ws, p := newWS(t)
	old := "def release(self, sku, qty):\n    item = self._items[sku]\n    item.reserved -= qty"
	neu := "def release(self, sku, qty):\n    item = self._items[sku]\n    if qty > item.reserved:\n        raise ValueError('too much')\n    item.reserved -= qty"
	out, err := ws.replace("service.py", old, neu)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "indentation adjusted to 4 spaces") {
		t.Errorf("expected a reindent note, got %q", out)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(service,
		"    def release(self, sku, qty):\n        item = self._items[sku]\n        item.reserved -= qty\n",
		"    def release(self, sku, qty):\n        item = self._items[sku]\n        if qty > item.reserved:\n            raise ValueError('too much')\n        item.reserved -= qty\n", 1)
	if string(got) != want {
		t.Errorf("file after edit:\n%s\nwant:\n%s", got, want)
	}
}

// Negative control: an ambiguous block must still be refused, reindented or not.
func TestReplaceRefusesAmbiguousReindent(t *testing.T) {
	ws, p := newWS(t)
	_, err := ws.replace("service.py", "item = self._items[sku]\nitem.reserved -= qty", "x = 1")
	if err == nil || !strings.Contains(err.Error(), "matches 2 places") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != service {
		t.Error("file changed despite the error")
	}
}

// Negative control: lines that differ in content, not just indentation, never match.
func TestReplaceRejectsDifferentContent(t *testing.T) {
	ws, _ := newWS(t)
	_, err := ws.replace("service.py", "def release(self, sku, qty):\n    item = self._items[sku]\n    item.reserved += qty", "x")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
}

// Negative control: mixed shifts inside one block are not a uniform reindent.
func TestReplaceRejectsNonUniformShift(t *testing.T) {
	ws, _ := newWS(t)
	_, err := ws.replace("service.py", "def release(self, sku, qty):\n        item = self._items[sku]\n    item.reserved -= qty", "x")
	if err == nil {
		t.Fatal("expected an error for a block with inconsistent indentation")
	}
}

func TestProtectedAndEscapingPaths(t *testing.T) {
	ws, _ := newWS(t)
	for _, p := range []string{"tests/test_x.py", "../etc/passwd", "/etc/passwd"} {
		if _, err := ws.replace(p, "a", "b"); err == nil {
			t.Errorf("replace(%q) should be refused", p)
		}
	}
}

// A fake server that truncates like Ollama: it reports a prompt size capped
// well below what was sent.
func TestTruncationIsInfraError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		prompt := 3000 // first turn
		if calls > 1 {
			prompt = 2050 // shrinks although the conversation grew
		}
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c%d","type":"function","function":{"name":"list_files","arguments":"{}"}}]}}],"usage":{"prompt_tokens":%d,"completion_tokens":5}}`, calls, prompt)
	}))
	defer srv.Close()

	sum := runWithFakeModel(t, srv.URL, 1600) // the estimate (~1.3K) is within 80% of this window
	if sum.StopReason != "context truncated" || sum.Infra == "" {
		t.Fatalf("stop=%q infra=%q; want a truncation infra error", sum.StopReason, sum.Infra)
	}
}

func TestOversizedConversationIsInfraError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("model should not be called when the conversation is already over the window")
	}))
	defer srv.Close()

	sum := runWithFakeModel(t, srv.URL, 500) // the first prompt alone is over 500 tokens
	if sum.StopReason != "context window full" || sum.Infra == "" {
		t.Fatalf("stop=%q infra=%q; want a context-full infra error", sum.StopReason, sum.Infra)
	}
}

// runWithFakeModel runs the agent loop against a fake model with a stub
// runner that reports a failing test run, so no microVM is needed.
func runWithFakeModel(t *testing.T, url string, ctxTokens int) *Summary {
	t.Helper()
	return runWithFakeModelTask(t, url, func(task *verify.Task) {}, ctxTokens)
}

func runWithFakeModelTask(t *testing.T, url string, edit func(*verify.Task), ctx ...int) *Summary {
	t.Helper()
	ctxTokens := 0
	if len(ctx) > 0 {
		ctxTokens = ctx[0]
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, "tests"), 0o755)
	os.WriteFile(filepath.Join(repo, "a.py"), []byte("x = 1\n"), 0o644)
	runner := filepath.Join(dir, "runner.sh")
	os.WriteFile(runner, []byte(`#!/bin/sh
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
mkdir -p "$out"; echo "test_a (tests.t.C.test_a) ... FAIL" > "$out/stderr.log"; : > "$out/stdout.log"
echo '{"guest":{"exit_code":1,"timed_out":false,"run_ms":1},"error":null}'
`), 0o755)
	task := verify.Task{Name: "t", TestCmd: "true", Protect: []string{"tests/"}, RequiredTests: []string{"tests.t.C.test_a"}}
	edit(&task)
	sum, err := Run(context.Background(), Config{
		Model:  &model.Client{BaseURL: url, Model: "fake"},
		Verify: verify.Config{Runner: runner, Host: "test"}, MaxTurns: 12, MaxTestRuns: 1, ContextTokens: ctxTokens,
	}, task, repo, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// scriptedServer replies with the given tool calls, one per model turn.
func scriptedServer(t *testing.T, calls ...string) *httptest.Server {
	t.Helper()
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n >= len(calls) {
			t.Errorf("model called %d times; script has %d turns", n+1, len(calls))
			http.Error(w, "script exhausted", 500)
			return
		}
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c%d","type":"function","function":%s}]}}],"usage":{"prompt_tokens":%d,"completion_tokens":5}}`,
			n, calls[n], 1000+n)
		n++
	}))
}

func TestNoChangeTestRunIsFreeAndClaimAgainstOwnTestsIsRecorded(t *testing.T) {
	srv := scriptedServer(t,
		`{"name":"run_tests","arguments":"{}"}`,
		`{"name":"run_tests","arguments":"{}"}`,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = 2\"}"}`,
		`{"name":"run_tests","arguments":"{}"}`,
		`{"name":"submit","arguments":"{\"fixed\":true}"}`,
	)
	defer srv.Close()

	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.TestRuns != 1 {
		t.Errorf("test_runs = %d, want 1: runs with no changes must not spend the budget", sum.TestRuns)
	}
	if sum.LastOwnTest != "FAIL" || !sum.ClaimAgainstOwnTests {
		t.Errorf("last_own_test=%q claim_against_own_tests=%v; want FAIL, true", sum.LastOwnTest, sum.ClaimAgainstOwnTests)
	}
	if sum.Harness != HarnessVersion {
		t.Errorf("harness = %q, want %q", sum.Harness, HarnessVersion)
	}
}

func TestBudgetParkedIsInfraAndCarriesResumeTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"budget_parked","message":"spent","resume_at":"2026-09-25T00:00:00+00:00"}}`)
	}))
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.StopReason != "budget parked" || sum.ParkedUntil != "2026-09-25T00:00:00+00:00" || sum.Infra == "" {
		t.Fatalf("stop=%q parked_until=%q infra=%q", sum.StopReason, sum.ParkedUntil, sum.Infra)
	}
}

// Negative control: an ordinary provider 429 is an infra error, but not a park.
func TestPlain429IsNotAPark(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"rate_limit","message":"slow down"}}`)
	}))
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.StopReason != "model error" || sum.ParkedUntil != "" || sum.Infra == "" {
		t.Fatalf("stop=%q parked_until=%q infra=%q", sum.StopReason, sum.ParkedUntil, sum.Infra)
	}
}

// A model that keeps producing unparseable tool calls fails the run: it is not
// excused as an infrastructure error.
func TestMalformedReplyIsScoredNotExcused(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"message":"XML syntax error on line 3: element <function> closed by </parameter>"}}`)
	}))
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.Infra != "" || sum.MalformedReplies != 3 || calls != 3 || sum.Verdict == "PASS" {
		t.Fatalf("infra=%q malformed=%d calls=%d verdict=%s; want scored failure after 3 attempts",
			sum.Infra, sum.MalformedReplies, calls, sum.Verdict)
	}
}

// Negative control: an ordinary 500 (the server's fault) stays an infra error.
func TestPlain500IsInfra(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"message":"model runner has unexpectedly stopped"}}`)
	}))
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.Infra == "" || sum.MalformedReplies != 0 {
		t.Fatalf("infra=%q malformed=%d; want an infra error", sum.Infra, sum.MalformedReplies)
	}
}

// On a hidden-test task, a fixed=true submit with no run_python since the last
// edit gets exactly one reminder; after a reproduction the submit is accepted.
func TestSubmitReminderOnHiddenTestTask(t *testing.T) {
	srv := scriptedServer(t,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = 2\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":true}"}`,
		`{"name":"run_python","arguments":"{\"code\":\"print(1)\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":true}"}`,
	)
	defer srv.Close()
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.patch")
	os.WriteFile(tp, []byte("--- /dev/null\n+++ b/tests/test_new.py\n@@ -0,0 +1 @@\n+x = 1\n"), 0o644)
	sum := runWithFakeModelTask(t, srv.URL, func(task *verify.Task) { task.TestPatch = tp })
	if sum.SubmitReminders != 1 || !sum.AgentClaimed || sum.PythonRuns != 1 || sum.Turns != 4 {
		t.Fatalf("reminders=%d claimed=%v python_runs=%d turns=%d; want 1, true, 1, 4",
			sum.SubmitReminders, sum.AgentClaimed, sum.PythonRuns, sum.Turns)
	}
}

func TestReplaceLines(t *testing.T) {
	ws, p := newWS(t)
	out, err := ws.replaceLines("service.py", 3, 4, "        item = self._items.get(sku)\n        item.reserved -= abs(qty)")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "        item = self._items.get(sku)\n        item.reserved -= abs(qty)\n\n    def ship") {
		t.Errorf("file after replace_lines:\n%s", got)
	}
	if !strings.Contains(out, "replaced lines 3-4") {
		t.Errorf("output %q", out)
	}
	for _, c := range []struct{ start, end int }{{0, 1}, {5, 4}, {3, 999}} {
		if _, err := ws.replaceLines("service.py", c.start, c.end, "x"); err == nil {
			t.Errorf("range %d-%d accepted", c.start, c.end)
		}
	}
	if _, err := ws.replaceLines("tests/t.py", 1, 1, "x"); err == nil {
		t.Error("protected path accepted")
	}
}

func TestStuckOnRepeatedFailingCall(t *testing.T) {
	bad := `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"nope\",\"new_text\":\"y\"}"}`
	srv := scriptedServer(t, bad, bad, bad, bad, bad, bad)
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if !strings.HasPrefix(sum.StopReason, "stuck:") || sum.Turns != 6 || sum.LoopWarnings != 3 || sum.Infra != "" {
		t.Fatalf("stop=%q turns=%d warnings=%d infra=%q", sum.StopReason, sum.Turns, sum.LoopWarnings, sum.Infra)
	}
}

// Negative control: different failing calls are not a loop.
func TestDifferentFailingCallsAreNotALoop(t *testing.T) {
	mk := func(s string) string {
		return `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"` + s + `\",\"new_text\":\"y\"}"}`
	}
	srv := scriptedServer(t, mk("a"), mk("b"), mk("c"), mk("d"), mk("e"), mk("f"), `{"name":"submit","arguments":"{\"fixed\":false}"}`)
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.StopReason != "agent submitted" || sum.LoopWarnings != 0 {
		t.Fatalf("stop=%q warnings=%d; want a normal submit and no warnings", sum.StopReason, sum.LoopWarnings)
	}
}

// The loop seen in 0.5 on django__django-14373: a cycle of successful
// read-only calls. It must end as stuck.
func TestCycleOfReadOnlyCallsIsStuck(t *testing.T) {
	a := `{"name":"search","arguments":"{\"pattern\":\"x\"}"}`
	b := `{"name":"read_file","arguments":"{\"path\":\"a.py\"}"}`
	srv := scriptedServer(t, a, b, a, b, a, b, a, b, a)
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if !strings.Contains(sum.StopReason, "no edit in between") || sum.Turns != 9 {
		t.Fatalf("stop=%q turns=%d", sum.StopReason, sum.Turns)
	}
}

// Negative control: the same read repeated around edits is normal work.
func TestRepeatedReadsAroundEditsAreFine(t *testing.T) {
	r := `{"name":"read_file","arguments":"{\"path\":\"a.py\"}"}`
	e1 := `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = 2\"}"}`
	e2 := `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 2\",\"new_text\":\"x = 3\"}"}`
	e3 := `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 3\",\"new_text\":\"x = 4\"}"}`
	srv := scriptedServer(t, r, r, e1, r, r, e2, r, r, e3, `{"name":"submit","arguments":"{\"fixed\":false}"}`)
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.StopReason != "agent submitted" || sum.LoopWarnings != 0 {
		t.Fatalf("stop=%q warnings=%d", sum.StopReason, sum.LoopWarnings)
	}
}

// Negative control (0.6.0 false positive): a small drop in the reported count
// far below the window is not truncation.
func TestSmallDropFarFromWindowIsNotTruncation(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		prompt := 13326
		if calls > 1 {
			prompt = 12771
		}
		if calls > 2 {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"s","type":"function","function":{"name":"submit","arguments":"{\"fixed\":false}"}}]}}],"usage":{"prompt_tokens":13000,"completion_tokens":5}}`)
			return
		}
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c%d","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}],"usage":{"prompt_tokens":%d,"completion_tokens":5}}`, calls, prompt)
	}))
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 250000)
	if sum.StopReason != "agent submitted" || sum.Infra != "" {
		t.Fatalf("stop=%q infra=%q; a 4%% drop at 13K of 250K is not truncation", sum.StopReason, sum.Infra)
	}
}

func TestSyntaxCheckAfterEdit(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "m.py"), []byte("x = {\n    'a': 1,\n}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("}\n"), 0o644)
	ws := &workspace{root: root}
	if msg := ws.syntaxError("m.py"); msg != "" {
		t.Fatalf("valid file reported: %q", msg)
	}
	ws.replaceLines("m.py", 2, 2, "    'a': 1,\n}")
	if msg := ws.syntaxError("m.py"); !strings.Contains(msg, "line") {
		t.Errorf("broken file not reported: %q", msg)
	}
	if msg := ws.syntaxError("notes.txt"); msg != "" {
		t.Errorf("non-Python file checked: %q", msg)
	}
}

// Wired into the loop: an edit that breaks a .py file is flagged to the model.
func TestSyntaxWarningReachesModel(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	srv := scriptedServer(t,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = (\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`,
	)
	defer srv.Close()
	sum := runWithFakeModelTask(t, srv.URL, func(*verify.Task) {})
	if sum.SyntaxErrorsIntroduced != 1 {
		t.Fatalf("syntax_errors_introduced = %d, want 1", sum.SyntaxErrorsIntroduced)
	}
}

func hiddenTestPatch(t *testing.T) func(*verify.Task) {
	t.Helper()
	tp := filepath.Join(t.TempDir(), "test.patch")
	os.WriteFile(tp, []byte("--- /dev/null\n+++ b/tests/test_new.py\n@@ -0,0 +1 @@\n+x = 1\n"), 0o644)
	return func(task *verify.Task) { task.TestPatch = tp }
}

// 0.8: three reproductions without an edit earn one commit nudge.
func TestCommitNudgeAfterReproductions(t *testing.T) {
	py := `{"name":"run_python","arguments":"{\"code\":\"print(1)\"}"}`
	srv := scriptedServer(t, py, py, py, `{"name":"submit","arguments":"{\"fixed\":false}"}`)
	defer srv.Close()
	sum := runWithFakeModelTask(t, srv.URL, hiddenTestPatch(t))
	if sum.CommitNudges != 1 {
		t.Fatalf("commit_nudges = %d, want 1", sum.CommitNudges)
	}
}

// Negative control: once the agent has edited, reproductions are normal work.
func TestNoCommitNudgeAfterAnEdit(t *testing.T) {
	py := `{"name":"run_python","arguments":"{\"code\":\"print(1)\"}"}`
	edit := `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = 2\"}"}`
	srv := scriptedServer(t, edit, py, py, py, `{"name":"submit","arguments":"{\"fixed\":false}"}`)
	defer srv.Close()
	sum := runWithFakeModelTask(t, srv.URL, hiddenTestPatch(t))
	if sum.CommitNudges != 0 {
		t.Fatalf("commit_nudges = %d after an edit, want 0", sum.CommitNudges)
	}
}

// Negative control: tasks with visible tests (fixtures) are not nudged.
func TestNoCommitNudgeOnVisibleTestTasks(t *testing.T) {
	py := `{"name":"run_python","arguments":"{\"code\":\"print(1)\"}"}`
	srv := scriptedServer(t, py, py, py, `{"name":"submit","arguments":"{\"fixed\":false}"}`)
	defer srv.Close()
	sum := runWithFakeModel(t, srv.URL, 0)
	if sum.CommitNudges != 0 {
		t.Fatalf("commit_nudges = %d on a visible-test task, want 0", sum.CommitNudges)
	}
}

// 0.9 live mode: create_file makes new files only; under a protected
// directory, only a new test_brokkr_*.py regression test.
func TestCreateFileRules(t *testing.T) {
	dir := t.TempDir()
	orig, work := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, d := range []string{orig, work} {
		os.MkdirAll(filepath.Join(d, "tests"), 0o755)
		os.WriteFile(filepath.Join(d, "tests/test_m.py"), []byte("x\n"), 0o644)
		os.WriteFile(filepath.Join(d, "m.py"), []byte("x\n"), 0o644)
	}
	w := &workspace{root: work, orig: orig, protect: []string{"tests/"}, newTests: "tests/"}
	cases := []struct {
		path string
		ok   bool
	}{
		{"tests/test_brokkr_issue.py", true},
		{"tests/sub/test_brokkr_deep.py", true},
		{"newmod.py", true},
		// Negative controls.
		{"tests/test_m.py", false},            // overwrite an existing test
		{"tests/test_other.py", false},        // a new test not named test_brokkr_*
		{"tests/conftest.py", false},          // fixtures would change every test
		{"m.py", false},                       // create_file never overwrites
		{"../escape.py", false},               // outside the repository
		{"tests/test_brokkr_issue.py", false}, // exists now
	}
	for _, c := range cases {
		out, refused := w.call("create_file", map[string]any{"path": c.path, "content": "def test_a():\n    assert True\n"})
		if ok := strings.HasPrefix(out, "ok:"); ok != c.ok {
			t.Errorf("create_file %s: %q (refused=%v), want ok=%v", c.path, out, refused, c.ok)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(work, "tests/test_m.py")); string(b) != "x\n" {
		t.Fatalf("existing test changed: %q", b)
	}
	if got := w.newTestFiles(); !reflect.DeepEqual(got, []string{"tests/sub/test_brokkr_deep.py", "tests/test_brokkr_issue.py"}) {
		t.Fatalf("newTestFiles = %v", got)
	}
}

// Tasks that are not live do not get create_file: 0.9 behaves as 0.8 there.
func TestCreateFileOnlyInLiveMode(t *testing.T) {
	for _, tl := range toolDefs(false) {
		if tl.Function.Name == "create_file" {
			t.Fatal("create_file offered on a non-live task")
		}
	}
	w := &workspace{root: t.TempDir(), orig: t.TempDir()}
	if out, refused := w.call("create_file", map[string]any{"path": "x.py", "content": ""}); !refused {
		t.Fatalf("create_file on a non-live workspace: %q", out)
	}
	n := 0
	for _, tl := range toolDefs(true) {
		if tl.Function.Name == "create_file" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("live tools have %d create_file", n)
	}
}

// runLive runs a live task end to end with a scripted model and a fake
// sandbox that reports tests from the staged repository: the regression test
// passes only when the fix (x = 2) is in a.py.
func runLive(t *testing.T, calls ...string) *Summary {
	t.Helper()
	srv := scriptedServer(t, calls...)
	defer srv.Close()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, "tests"), 0o755)
	os.WriteFile(filepath.Join(repo, "a.py"), []byte("x = 1\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "tests/test_m.py"), []byte("def test_old(): pass\n"), 0o644)
	runner := filepath.Join(dir, "runner.sh")
	os.WriteFile(runner, []byte(`#!/bin/sh
while [ $# -gt 0 ]; do case "$1" in --out) out=$2;; --repo) repo=$2;; esac; shift; done
mkdir -p "$out"; : > "$out/stderr.log"
{ echo "PASSED tests/test_m.py::test_old"
  if [ -f "$repo/tests/test_brokkr_x.py" ]; then
    if grep -q "x = 2" "$repo/a.py"; then echo "PASSED tests/test_brokkr_x.py::test_x"; else echo "FAILED tests/test_brokkr_x.py::test_x - assert"; fi
  fi
  [ -f "$repo/tests/test_brokkr_y.py" ] && echo "PASSED tests/test_brokkr_y.py::test_y"; } > "$out/stdout.log"
echo '{"guest":{"exit_code":0,"timed_out":false,"run_ms":1},"error":null}'
`), 0o755)
	task := verify.Task{Name: "live", TestCmd: "pytest tests/test_m.py", Protect: []string{"tests/"}, Parser: "pytest",
		Live: true, NewTestsDir: "tests/"}
	sum, err := Run(context.Background(), Config{
		Model:  &model.Client{BaseURL: srv.URL, Model: "fake"},
		Verify: verify.Config{Runner: runner, Host: "test"}, MaxTurns: 12, MaxTestRuns: 3,
	}, task, repo, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

const (
	liveTest   = `{"name":"create_file","arguments":"{\"path\":\"tests/test_brokkr_x.py\",\"content\":\"def test_x(): ...\\n\"}"}`
	liveFix    = `{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = 2\"}"}`
	liveSubmit = `{"name":"submit","arguments":"{\"fixed\":false}"}`
)

func TestLiveModePassesWithTestAndFix(t *testing.T) {
	sum := runLive(t, liveTest, liveFix, liveSubmit)
	if sum.Verdict != verify.Pass || sum.SelfTest != verify.Pass {
		t.Fatalf("verdict %s self_test %s: %v", sum.Verdict, sum.SelfTest, sum.Reasons)
	}
}

// Negative controls: a fix with no test, and a test with no fix.
func TestLiveModeFailsWithoutARegressionTest(t *testing.T) {
	if sum := runLive(t, liveFix, liveSubmit); sum.Verdict != verify.Fail {
		t.Fatalf("verdict %s, want FAIL: %v", sum.Verdict, sum.Reasons)
	}
}

func TestLiveModeFailsWhenTheTestDoesNotPassAfter(t *testing.T) {
	if sum := runLive(t, liveTest, liveSubmit); sum.Verdict != verify.Fail {
		t.Fatalf("verdict %s, want FAIL: %v", sum.Verdict, sum.Reasons)
	}
}

// A test that passes on the unfixed code is no evidence, even with a real fix
// beside it.
func TestLiveModeFailsWhenTheTestPassesBefore(t *testing.T) {
	always := `{"name":"create_file","arguments":"{\"path\":\"tests/test_brokkr_y.py\",\"content\":\"def test_y(): pass\\n\"}"}`
	sum := runLive(t, always, liveFix, liveSubmit)
	if sum.Verdict != verify.Fail || !strings.Contains(strings.Join(sum.Reasons, " "), "already passes without the fix") {
		t.Fatalf("verdict %s, want FAIL because the test passes before: %v", sum.Verdict, sum.Reasons)
	}
}

// A routed run: the primary is parked, so the whole run is served by the
// fallback, and the summary says so.
func TestRoutedRunFallsBackAndRecordsIt(t *testing.T) {
	parked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"type":"budget_parked","message":"spent","resume_at":"2099-01-01T00:00:00Z"}}`)
	}))
	defer parked.Close()
	srv := scriptedServer(t,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"x = 1\",\"new_text\":\"x = 2\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`,
	)
	defer srv.Close()
	r := route.New([]route.Backend{
		{Name: "codestral", BaseURL: parked.URL, Model: "codestral", ContextTokens: 256000},
		{Name: "qwen", BaseURL: srv.URL, Model: "qwen", ContextTokens: 32768},
	})
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, "tests"), 0o755)
	os.WriteFile(filepath.Join(repo, "a.py"), []byte("x = 1\n"), 0o644)
	runner := filepath.Join(dir, "runner.sh")
	os.WriteFile(runner, []byte(`#!/bin/sh
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
mkdir -p "$out"; echo "test_a (tests.t.C.test_a) ... FAIL" > "$out/stderr.log"; : > "$out/stdout.log"
echo '{"guest":{"exit_code":1,"timed_out":false,"run_ms":1},"error":null}'
`), 0o755)
	sum, err := Run(context.Background(), Config{
		Model: r.Primary().Client(), Router: r,
		Verify: verify.Config{Runner: runner, Host: "test"}, MaxTurns: 12, MaxTestRuns: 1, ContextTokens: 256000,
	}, verify.Task{Name: "t", TestCmd: "true", Protect: []string{"tests/"}, RequiredTests: []string{"tests.t.C.test_a"}}, repo, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if sum.ParkedUntil != "" || sum.Infra != "" || sum.StopReason != "agent submitted" {
		t.Fatalf("parked=%q infra=%q stop=%q", sum.ParkedUntil, sum.Infra, sum.StopReason)
	}
	if sum.ServedBy["qwen"] != 2 || len(sum.RouteSwitches) != 1 || sum.RouteSwitches[0].To != "qwen" {
		t.Fatalf("served_by %v switches %+v", sum.ServedBy, sum.RouteSwitches)
	}
}
