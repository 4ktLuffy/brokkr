package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/4ktLuffy/brokkr/internal/model"
)

func msg(role, content string, calls ...model.ToolCall) model.Message {
	return model.Message{Role: role, Content: content, ToolCalls: calls}
}

func call(id, name string, args map[string]any) model.ToolCall {
	var c model.ToolCall
	c.ID, c.Type = id, "function"
	c.Function.Name = name
	b, _ := json.Marshal(args)
	c.Function.Arguments = string(b)
	return c
}

func tool(id, name, content string) model.Message {
	return model.Message{Role: "tool", Content: content, ToolCallID: id, Name: name}
}

func writeJSONL(t *testing.T, path string, msgs []model.Message) {
	t.Helper()
	var b bytes.Buffer
	for _, m := range msgs {
		l, _ := json.Marshal(m)
		b.Write(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

const footer = "\n[turn %d of 40; sandbox runs %d of 10]"

// synth builds a run directory with every kind of step: reads, a failed edit,
// a good edit, a commit nudge, a loop warning, a python run, run_tests, a
// submit reminder, and a reply with no tool call.
func synth(t *testing.T) string {
	dir := t.TempDir()
	msgs := []model.Message{
		msg("system", "system prompt"),
		msg("user", "Issue t-1: mean is off by one"),
		msg("assistant", "I will look.", call("c1", "search", map[string]any{"pattern": "mean"})),
		tool("c1", "search", "calc.py:3: def mean"+fmt.Sprintf(footer, 1, 0)),
		msg("assistant", "Reading.", call("c2", "read_file", map[string]any{"path": "calc.py"})),
		tool("c2", "read_file", "1 def mean(xs):\n2   return sum(xs)/(len(xs)-1)"+
			"\n\nYou have not changed any file yet, and a third of your turns are used. You know enough to try."+fmt.Sprintf(footer, 2, 0)),
		msg("assistant", "Bad edit.", call("c3", "replace_in_file", map[string]any{"path": "calc.py", "old_text": "nope", "new_text": "x"})),
		tool("c3", "replace_in_file", "error: old_text not found"+"\n\nYou have sent this exact call 3 times and it failed each time. Do something different."+fmt.Sprintf(footer, 3, 0)),
		msg("assistant", "Good edit.", call("c4", "replace_in_file", map[string]any{"path": "calc.py", "old_text": "def mean(xs):\n    return sum(xs)/(len(xs)-1)\n", "new_text": "def mean(xs):\n    return sum(xs)/len(xs)\n"})),
		tool("c4", "replace_in_file", "ok: replaced"+fmt.Sprintf(footer, 4, 0)),
		msg("assistant", "Checking.", call("c5", "run_python", map[string]any{"code": "print(1)"})),
		tool("c5", "run_python", "exit 0\n1"+fmt.Sprintf(footer, 5, 1)),
		msg("assistant", "Tests.", call("c6", "run_tests", map[string]any{})),
		tool("c6", "run_tests", "verdict: PASS (ok)\nOK"+fmt.Sprintf(footer, 6, 2)),
		msg("assistant", "Lines edit and a new file.", call("c7", "replace_lines", map[string]any{"path": "calc.py", "start_line": 1, "end_line": 2, "new_text": "def mean(xs):\n    return 0\n"}),
			call("c8", "create_file", map[string]any{"path": "tests/test_brokkr_x.py", "content": "def test_x():\n    assert True\n"})),
		tool("c7", "replace_lines", "ok: replaced lines"+fmt.Sprintf(footer, 7, 2)),
		tool("c8", "create_file", "ok: created"+fmt.Sprintf(footer, 7, 2)),
		msg("assistant", "just talking"),
		msg("user", noToolCallNudge),
		msg("assistant", "Submitting.", call("c9", "submit", map[string]any{"fixed": true, "summary": "fixed"})),
		tool("c9", "submit", "Not submitted yet: you have not run a reproduction with run_python since your last edit."+fmt.Sprintf(footer, 9, 2)),
		msg("assistant", "Submitting again.", call("c10", "submit", map[string]any{"fixed": true, "summary": "fixed"})),
	}
	writeJSONL(t, filepath.Join(dir, "transcript.jsonl"), msgs)
	put(t, filepath.Join(dir, "summary.json"), `{"task":"t-1","model":"m","harness":"0.9.0","verdict":"FAIL","reasons":["2 required tests failed"],
"agent_claimed_fixed":true,"claim_against_own_tests":false,"stop_reason":"agent submitted","turns":11,"test_runs":1,"python_runs":1,
"prompt_tokens":1000,"completion_tokens":100,"wall_ms":65000,"budget":{"max_turns":40,"max_sandbox_runs":10,"compact_above":16000},
"sampling":{"context_tokens":32768},"served_by":{"a":6,"b":5}}`)
	put(t, filepath.Join(dir, "final.patch"), "--- a/calc.py\n+++ b/calc.py\n@@ -1,2 +1,2 @@\n def mean(xs):\n-    return sum(xs)/(len(xs)-1)\n+    return sum(xs)/len(xs)\n")
	put(t, filepath.Join(dir, "final", "evidence.json"), `{"verifier":"0.4.1","run_id":"r","task":{"issue":"mean off by one","required_tests":["a","b","c"]},
"inputs":{"patch_sha256":"abc123"},"verdict":"FAIL","reasons":["2 required tests failed"],"tests":{"passed":["a"],"failed":["b"],"missing_required":["c"]},"sandbox":{"kernel_sha256":"k1"}}`)
	put(t, filepath.Join(dir, "attempt-1", "result.json"), `{"exit_code":0,"timed_out":false,"run_ms":1200}`)
	put(t, filepath.Join(dir, "attempt-1", "stdout.log"), "ran 5 tests\nOK\n")
	put(t, filepath.Join(dir, "python-1", "result.json"), `{"exit_code":3,"timed_out":false,"run_ms":900}`)
	put(t, filepath.Join(dir, "python-1", "stdout.log"), "line\n")
	put(t, filepath.Join(dir, "python-1", "stderr.log"), "Traceback boom\n")
	return dir
}

func render(t *testing.T, runs ...*Run) string {
	t.Helper()
	var b bytes.Buffer
	if err := Render(&b, runs...); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSyntheticStructure(t *testing.T) {
	r, err := Load(synth(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var turns, harness int
	for _, it := range r.Items {
		switch it.Kind {
		case "turn":
			turns++
		case "harness":
			harness++
		}
	}
	if turns != 10 || harness != 1 {
		t.Fatalf("turns=%d harness=%d, want 10 and 1", turns, harness)
	}
	if len(r.Estimate) != 10 {
		t.Fatalf("estimate points %d", len(r.Estimate))
	}
	if !r.OverClaim {
		t.Error("claimed fixed with verdict FAIL must be an over-claim")
	}
	if r.Window != 32768 || r.MaxTurns != 40 || r.ServedBy["a"] != 6 {
		t.Errorf("header fields: %+v", r)
	}
	if r.Evidence == nil || r.Evidence.Required != 3 || r.Evidence.RequiredPassed != 1 || len(r.Evidence.MissingRequired) != 1 {
		t.Errorf("evidence: %+v", r.Evidence)
	}
	if r.Issue != "mean off by one" {
		t.Errorf("issue %q", r.Issue)
	}
	if len(r.Notes) != 0 {
		t.Errorf("a complete run should have no notes: %v", r.Notes)
	}
}

func TestEditDiffs(t *testing.T) {
	r, _ := Load(synth(t), Options{})
	var edits []Call
	for _, it := range r.Items {
		for _, c := range it.Calls {
			if c.Phase == "edit" {
				edits = append(edits, c)
			}
		}
	}
	if len(edits) != 4 {
		t.Fatalf("%d edit calls", len(edits))
	}
	if !edits[0].Failed {
		t.Error("the failed edit must be marked failed")
	}
	got := ""
	for _, l := range edits[1].Diff.Lines {
		got += l.Op + l.Text + "|"
	}
	if got != " def mean(xs):|-    return sum(xs)/(len(xs)-1)|+    return sum(xs)/len(xs)|" {
		t.Errorf("replace diff: %q", got)
	}
	if edits[2].Diff.Kind != "lines" || len(edits[2].Diff.Lines) != 2 || edits[2].Diff.Lines[0].Op != "+" {
		t.Errorf("replace_lines diff: %+v", edits[2].Diff)
	}
	if edits[3].Diff.Kind != "create" || edits[3].Diff.Path != "tests/test_brokkr_x.py" {
		t.Errorf("create_file diff: %+v", edits[3].Diff)
	}
	// negative control: a read is not an edit and carries no diff
	for _, it := range r.Items {
		for _, c := range it.Calls {
			if c.Phase != "edit" && c.Diff != nil {
				t.Errorf("%s has a diff", c.Name)
			}
		}
	}
}

func TestInterventionMarkers(t *testing.T) {
	r, _ := Load(synth(t), Options{})
	kinds := map[string]int{}
	for _, it := range r.Items {
		for _, m := range it.Markers {
			kinds[m.Kind]++
		}
		for _, c := range it.Calls {
			for _, m := range c.Markers {
				kinds[m.Kind]++
			}
		}
	}
	for _, k := range []string{"commit_nudge", "loop_warning", "submit_reminder", "no_tool_call"} {
		if kinds[k] != 1 {
			t.Errorf("marker %s: %d, want 1 (%v)", k, kinds[k], kinds)
		}
	}
	// The nudge text is separated from the tool's own output.
	c := r.Items[1].Calls[0]
	if strings.Contains(c.Result, "You have not changed") || strings.Contains(c.Result, "[turn 2 of 40") {
		t.Errorf("nudge or budget footer left in the result: %q", c.Result)
	}
	if r.Items[1].Budget != "turn 2 of 40; sandbox runs 0 of 10" {
		t.Errorf("budget %q", r.Items[1].Budget)
	}
	// negative control: ordinary results get no marker
	if len(r.Items[0].Calls[0].Markers) != 0 {
		t.Error("a plain search result must have no marker")
	}
}

func TestSandboxLinks(t *testing.T) {
	r, _ := Load(synth(t), Options{})
	var py, tests *Sandbox
	for _, it := range r.Items {
		for _, c := range it.Calls {
			switch c.Name {
			case "run_python":
				py = c.Sandbox
			case "run_tests":
				tests = c.Sandbox
			}
		}
	}
	if py == nil || py.Dir != "python-1" || py.ExitCode == nil || *py.ExitCode != 3 || py.Stderr != "Traceback boom" {
		t.Errorf("python sandbox: %+v", py)
	}
	// attempt numbers count run_python and run_tests together: the second
	// sandbox call is attempt-2, which the synthetic dir does not have.
	if tests == nil || !strings.Contains(tests.Dir, "attempt-2") || !strings.Contains(tests.Dir, "not found") {
		t.Errorf("tests sandbox: %+v", tests)
	}
}

func TestPageIsSelfContained(t *testing.T) {
	r, _ := Load(synth(t), Options{})
	page := render(t, r)
	for _, bad := range []string{"src=\"http", "href=\"http", "@import", "url(http", "<link", "<img", "innerHTML", "fetch(", "XMLHttpRequest"} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if !strings.Contains(page, `type="application/json"`) || !strings.Contains(page, "prefers-color-scheme") {
		t.Error("missing data block or dark theme")
	}
	// negative control for the check above: a page that did fetch would be caught
	if !strings.Contains("x fetch(y)", "fetch(") {
		t.Fatal("control")
	}
}

var scriptTag = regexp.MustCompile(`(?i)<script`)

// A hostile transcript must not add a script element or end the data block.
func TestHostileTranscriptCannotInjectScript(t *testing.T) {
	evil := `<script>alert(1)</script></script><!-- <script> <img src=x onerror=alert(2)>   `
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "transcript.jsonl"), []model.Message{
		msg("system", evil),
		msg("user", evil),
		msg("assistant", evil, call("c1", "search", map[string]any{"pattern": evil})),
		tool("c1", "search", evil),
	})
	put(t, filepath.Join(dir, "summary.json"), `{"task":"`+strings.ReplaceAll(evil, `"`, ``)+`","model":"</script><b>m","verdict":"FAIL","stop_reason":"</script>"}`)
	put(t, filepath.Join(dir, "final.patch"), "+"+evil+"\n")
	put(t, filepath.Join(dir, "python-1", "stdout.log"), evil)
	r, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	page := render(t, r)

	// Exactly our three script elements (data, code) and nothing from the run.
	if n := len(scriptTag.FindAllString(page, -1)); n != 2 {
		t.Fatalf("%d <script tags in the page, want 2 (data block and code)", n)
	}
	if n := strings.Count(strings.ToLower(page), "</script>"); n != 2 {
		t.Fatalf("%d </script> in the page, want 2", n)
	}
	start := strings.Index(page, `<script id="run-data"`)
	end := strings.Index(page[start:], "</script>")
	block := page[start : start+end]
	if strings.ContainsAny(block[strings.Index(block, ">")+1:], "<>&") {
		t.Error("data block contains a raw <, > or &")
	}
	outside := page[:start] + page[start+end:]
	for _, bad := range []string{"<b>m", "<img", "<script>alert"} {
		if strings.Contains(outside, bad) {
			t.Errorf("run text %q reached the HTML as markup", bad)
		}
	}
	if !strings.Contains(page[:start], "&lt;script&gt;alert(1)") {
		t.Error("the title should carry the task name escaped")
	}
	// the JSON must still round-trip to the original text
	js := block[strings.Index(block, ">")+1:]
	var back struct{ Runs []Run }
	if err := json.Unmarshal([]byte(js), &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Runs[0].Items[0].Text; got != evil {
		t.Errorf("round trip changed the text: %q", got)
	}
	// the app code never uses innerHTML or document.write
	for _, bad := range []string{"innerHTML", "outerHTML", "document.write", "insertAdjacentHTML", "eval("} {
		if strings.Contains(appJS, bad) {
			t.Errorf("app.js uses %s", bad)
		}
	}
	// negative control: a naive template would have let the tag through
	naive := "<script id=\"run-data\" type=\"application/json\">" + evil + "</script>"
	if len(scriptTag.FindAllString(naive, -1)) < 2 {
		t.Fatal("negative control failed: the naive page should show an injected script tag")
	}
}

func TestMissingOptionalFiles(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "transcript.jsonl"), []model.Message{
		msg("system", "s"), msg("user", "issue"),
		msg("assistant", "hi", call("c1", "run_tests", map[string]any{})),
		tool("c1", "run_tests", "verdict: FAIL"),
	})
	r, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Empty || len(r.Items) != 1 {
		t.Fatalf("empty=%v items=%d", r.Empty, len(r.Items))
	}
	notes := strings.Join(r.Notes, "\n")
	for _, want := range []string{"summary.json", "final.patch", "final/evidence.json"} {
		if !strings.Contains(notes, want) {
			t.Errorf("no note about %s: %v", want, r.Notes)
		}
	}
	if r.Items[0].Calls[0].Sandbox == nil || !strings.Contains(r.Items[0].Calls[0].Sandbox.Dir, "not found") {
		t.Error("a missing attempt dir must be said, not hidden")
	}
	if page := render(t, r); !strings.Contains(page, "run-data") {
		t.Error("page did not render")
	}
}

func TestEmptyTranscript(t *testing.T) {
	for name, setup := range map[string]func(string){
		"empty file": func(d string) { put(t, filepath.Join(d, "transcript.jsonl"), "") },
		"no file":    func(string) {},
		"garbage":    func(d string) { put(t, filepath.Join(d, "transcript.jsonl"), "not json\n{\n") },
	} {
		d := t.TempDir()
		setup(d)
		r, err := Load(d, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !r.Empty || len(r.Items) != 0 {
			t.Errorf("%s: Empty=%v items=%d", name, r.Empty, len(r.Items))
		}
		page := render(t, r)
		if !strings.Contains(page, `"empty":true`) || !strings.Contains(appJS, "no transcript") {
			t.Errorf("%s: the page does not carry the empty-run message", name)
		}
	}
	// negative control: a run with a transcript is not empty
	r, _ := Load(synth(t), Options{})
	if r.Empty {
		t.Error("synthetic run reported empty")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Error("a missing directory must be an error")
	}
}

func TestTruncationOfLongResults(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("é", 5000)
	writeJSONL(t, filepath.Join(dir, "transcript.jsonl"), []model.Message{
		msg("user", "i"), msg("assistant", "", call("c1", "read_file", map[string]any{"path": "a"})), tool("c1", "read_file", long),
	})
	r, _ := Load(dir, Options{MaxResult: 1001})
	c := r.Items[0].Calls[0]
	if !c.Cut || c.ResultLen != len(long) || len(c.Result) > 1001 || !strings.Contains(strings.Join(r.Notes, ""), "truncated") {
		t.Errorf("cut=%v len=%d got=%d notes=%v", c.Cut, c.ResultLen, len(c.Result), r.Notes)
	}
	if strings.ContainsRune(c.Result, '�') {
		t.Error("truncation split a UTF-8 character")
	}
	r, _ = Load(dir, Options{})
	if r.Items[0].Calls[0].Cut {
		t.Error("a 10 KB result must not be cut at the default limit")
	}
}

func TestCompactionPoints(t *testing.T) {
	dir := t.TempDir()
	msgs := []model.Message{msg("user", "i")}
	for i := 0; i < 8; i++ {
		id := fmt.Sprint("c", i)
		msgs = append(msgs, msg("assistant", "", call(id, "read_file", map[string]any{"path": "a"})), tool(id, "read_file", strings.Repeat("x", 3000)))
	}
	writeJSONL(t, filepath.Join(dir, "transcript.jsonl"), msgs)
	put(t, filepath.Join(dir, "summary.json"), `{"task":"t","budget":{"compact_above":4000}}`)
	r, _ := Load(dir, Options{})
	n := 0
	for _, p := range r.Estimate {
		if p.Compacted {
			n++
		}
	}
	if n == 0 {
		t.Error("no compaction point found with compact_above 4000")
	}
	put(t, filepath.Join(dir, "summary.json"), `{"task":"t"}`)
	r, _ = Load(dir, Options{})
	for _, p := range r.Estimate {
		if p.Compacted {
			t.Error("compaction marked although compact_above is unset")
		}
	}
}

func TestCompareRendersBothRuns(t *testing.T) {
	a, _ := Load(synth(t), Options{})
	b, _ := Load(t.TempDir(), Options{})
	page := render(t, a, b)
	if strings.Count(page, `"task":`) < 2 || !strings.Contains(appJS, `"cols"`) {
		t.Error("compare page does not carry two runs")
	}
	if err := Render(&bytes.Buffer{}); err == nil {
		t.Error("rendering no runs must be an error")
	}
}
