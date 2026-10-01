package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4ktLuffy/brokkr/internal/model"
	"github.com/4ktLuffy/brokkr/internal/verify"
)

// codeRun runs the agent against a scripted fake model on a two-file repo and
// returns the summary, the tool names offered on the first request, the system
// prompt, and the tool results the model saw.
func codeRun(t *testing.T, codeTools bool, calls ...string) (*Summary, []string, string, []string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	var offered []string
	var system string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n == 0 {
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Tools []struct {
					Function struct{ Name string }
				}
				Messages []struct{ Role, Content string }
			}
			_ = json.Unmarshal(body, &req)
			for _, tl := range req.Tools {
				offered = append(offered, tl.Function.Name)
			}
			system = req.Messages[0].Content
		}
		if n >= len(calls) {
			t.Errorf("script exhausted")
			http.Error(w, "x", 500)
			return
		}
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c%d","type":"function","function":%s}]}}],"usage":{"prompt_tokens":%d,"completion_tokens":5}}`, n, calls[n], 1000+n)
		n++
	}))
	defer srv.Close()

	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, "tests"), 0o755)
	os.WriteFile(filepath.Join(repo, "a.py"), []byte("def total(xs):\n    return sum(xs)\n\n\nclass Cart:\n    def add(self, x):\n        return total([x])\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "b.py"), []byte("from a import total\n\nprint(total([1]))\n"), 0o644)
	runner := filepath.Join(dir, "runner.sh")
	os.WriteFile(runner, []byte(`#!/bin/sh
while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
mkdir -p "$out"; echo "test_a (tests.t.C.test_a) ... FAIL" > "$out/stderr.log"; : > "$out/stdout.log"
echo '{"guest":{"exit_code":1,"timed_out":false,"run_ms":1},"error":null}'
`), 0o755)
	task := verify.Task{Name: "t", TestCmd: "true", Protect: []string{"tests/"}, RequiredTests: []string{"tests.t.C.test_a"}}
	out := filepath.Join(dir, "out")
	sum, err := Run(context.Background(), Config{
		Model:  &model.Client{BaseURL: srv.URL, Model: "fake"},
		Verify: verify.Config{Runner: runner, Host: "test"}, MaxTurns: 12, MaxTestRuns: 1, CodeTools: codeTools,
	}, task, repo, out)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	f, _ := os.Open(filepath.Join(out, "transcript.jsonl"))
	dec := json.NewDecoder(f)
	for {
		var m model.Message
		if dec.Decode(&m) != nil {
			break
		}
		if m.Role == "tool" {
			results = append(results, m.Content)
		}
	}
	return sum, offered, system, results
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestCodeToolsOffDoesNotOfferOrAnswerThem(t *testing.T) {
	sum, offered, system, results := codeRun(t, false,
		`{"name":"find_definition","arguments":"{\"name\":\"total\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`)
	for _, name := range []string{"find_definition", "find_usages", "outline"} {
		if has(offered, name) {
			t.Errorf("%s offered with the flag off: %v", name, offered)
		}
	}
	if strings.Contains(system, "find_usages") {
		t.Error("system prompt mentions the code tools with the flag off")
	}
	if len(results) == 0 || !strings.Contains(results[0], "unknown tool") || sum.Refusals != 1 {
		t.Errorf("a call to an unoffered tool must be refused: %v refusals=%d", results, sum.Refusals)
	}
}

// With the flag off the tool list is exactly the old one, in the same order.
func TestCodeToolsOffToolListUnchanged(t *testing.T) {
	_, offered, _, _ := codeRun(t, false, `{"name":"submit","arguments":"{\"fixed\":false}"}`)
	want := []string{"list_dir", "search", "read_file", "replace_in_file", "replace_lines", "run_tests", "run_python", "submit"}
	if strings.Join(offered, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", offered, want)
	}
}

func TestCodeToolsOnOffersAndAnswers(t *testing.T) {
	sum, offered, system, results := codeRun(t, true,
		`{"name":"find_definition","arguments":"{\"name\":\"Cart.add\"}"}`,
		`{"name":"find_usages","arguments":"{\"name\":\"total\"}"}`,
		`{"name":"outline","arguments":"{\"path\":\"a.py\"}"}`,
		`{"name":"find_definition","arguments":"{\"name\":\"totl\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`)
	for _, name := range []string{"find_definition", "find_usages", "outline"} {
		if !has(offered, name) {
			t.Errorf("%s not offered: %v", name, offered)
		}
	}
	if !has(offered, "submit") || offered[3] != "find_definition" {
		t.Errorf("tools should follow read_file: %v", offered)
	}
	if !strings.Contains(system, "find_usages") {
		t.Error("system prompt should mention the code tools")
	}
	if len(results) != 4 {
		t.Fatalf("results: %v", results)
	}
	if !strings.Contains(results[0], "a.py:6-7") || !strings.Contains(results[0], "def add(self, x)") {
		t.Errorf("find_definition: %s", results[0])
	}
	if !strings.Contains(results[1], "b.py (") || !strings.Contains(results[1], "[call] return total([x])") {
		t.Errorf("find_usages: %s", results[1])
	}
	if !strings.Contains(results[2], "class Cart") || !strings.Contains(results[2], "def total(xs)") {
		t.Errorf("outline: %s", results[2])
	}
	// negative control: an unknown name is answered, with a suggestion
	if !strings.Contains(results[3], "Similar names: total") || sum.Refusals != 0 {
		t.Errorf("unknown name: %s", results[3])
	}
	if sum.ToolCalls["find_usages"] != 1 {
		t.Errorf("tool_calls: %v", sum.ToolCalls)
	}
}

func TestStaticWarningAfterEdit(t *testing.T) {
	sum, _, _, results := codeRun(t, true,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"return sum(xs)\",\"new_text\":\"return summ(xs)\"}"}`,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"return summ(xs)\",\"new_text\":\"return sum(xs) + len(xs)\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`)
	if !strings.Contains(results[0], "undefined name 'summ'") || sum.StaticWarnings != 1 {
		t.Errorf("typo not reported (warnings=%d): %s", sum.StaticWarnings, results[0])
	}
	// negative control: a correct edit, and the one after the repair, stay silent
	if strings.Contains(results[1], "WARNING") {
		t.Errorf("clean edit warned: %s", results[1])
	}
}

// The same typo with the flag off is not reported: behaviour is as before.
func TestStaticWarningOffByDefault(t *testing.T) {
	sum, _, _, results := codeRun(t, false,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"return sum(xs)\",\"new_text\":\"return summ(xs)\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`)
	if strings.Contains(results[0], "static check") || sum.StaticWarnings != 0 {
		t.Errorf("static check ran with the flag off: %s", results[0])
	}
}

// The index follows the agent's own edits.
func TestCodeToolsSeeEdits(t *testing.T) {
	_, _, _, results := codeRun(t, true,
		`{"name":"replace_in_file","arguments":"{\"path\":\"a.py\",\"old_text\":\"def total(xs):\",\"new_text\":\"def grand_total(xs):\"}"}`,
		`{"name":"find_definition","arguments":"{\"name\":\"grand_total\"}"}`,
		`{"name":"submit","arguments":"{\"fixed\":false}"}`)
	if !strings.Contains(results[1], "a.py:1-2") {
		t.Errorf("edited file not re-indexed: %s", results[1])
	}
}
