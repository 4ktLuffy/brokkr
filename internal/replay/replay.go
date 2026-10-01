// Package replay turns a finished `brokkr fix` run directory into one
// self-contained HTML page, a flight recorder a reviewer can scrub through:
// every turn, tool call, edit, sandbox run and harness intervention, the
// prompt size over time, the final patch and the verification evidence.
//
// Transcripts hold untrusted repository text and model output. The page
// therefore carries the run as escaped JSON in a <script type="application/json">
// block, and its script builds the DOM with textContent only; nothing from the
// run is ever parsed as HTML.
package replay

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/4ktLuffy/brokkr/internal/model"
)

//go:embed page.html
var pageTmpl string

//go:embed app.js
var appJS string

//go:embed style.css
var styleCSS string

// Options bound the size of the page. Zero values use the defaults.
type Options struct {
	MaxResult int // characters kept per tool result (default 20000)
	TailLines int // lines of a sandbox log kept (default 60)
	TailBytes int // bytes of a sandbox log kept (default 8000)
}

func (o Options) norm() Options {
	if o.MaxResult <= 0 {
		o.MaxResult = 20000
	}
	if o.TailLines <= 0 {
		o.TailLines = 60
	}
	if o.TailBytes <= 0 {
		o.TailBytes = 8000
	}
	return o
}

// Run is everything the page shows about one run.
type Run struct {
	Dir      string         `json:"dir"`
	Task     string         `json:"task"`
	Issue    string         `json:"issue,omitempty"`
	Model    string         `json:"model"`
	ServedBy map[string]int `json:"served_by,omitempty"`
	Harness  string         `json:"harness"`
	Host     string         `json:"host,omitempty"`
	Verdict  string         `json:"verdict"`
	Reasons  []string       `json:"reasons,omitempty"`
	Claimed  bool           `json:"claimed_fixed"`
	// OverClaim: the agent said it fixed the issue and the verdict is not PASS.
	OverClaim            bool           `json:"over_claim"`
	ClaimAgainstOwnTests bool           `json:"claim_against_own_tests"`
	LastOwnTest          string         `json:"last_own_test,omitempty"`
	StopReason           string         `json:"stop_reason"`
	Infra                string         `json:"infra_error,omitempty"`
	Turns                int            `json:"turns"`
	TestRuns             int            `json:"test_runs"`
	PythonRuns           int            `json:"python_runs"`
	PromptTok            int            `json:"prompt_tokens"`
	OutputTok            int            `json:"completion_tokens"`
	WallMS               int64          `json:"wall_ms"`
	MaxTurns             int            `json:"max_turns,omitempty"`
	MaxSandbox           int            `json:"max_sandbox_runs,omitempty"`
	Window               int            `json:"context_window,omitempty"`
	CompactAbove         int            `json:"compact_above,omitempty"`
	CompactBatches       int            `json:"compact_batches"`
	SelfTest             string         `json:"self_test,omitempty"`
	ToolCounts           map[string]int `json:"tool_calls,omitempty"`

	Items    []Item    `json:"items"`
	Estimate []Point   `json:"estimate"`
	Patch    string    `json:"patch"`
	Evidence *Evidence `json:"evidence,omitempty"`
	Baseline *Evidence `json:"baseline,omitempty"`
	Notes    []string  `json:"notes,omitempty"` // missing or unreadable inputs, truncation
	Empty    bool      `json:"empty"`
}

// Item is one step of the timeline: an assistant turn, or a harness message
// the model received between turns.
type Item struct {
	Kind    string   `json:"kind"` // "turn" or "harness"
	N       int      `json:"n,omitempty"`
	Text    string   `json:"text,omitempty"`
	Calls   []Call   `json:"calls,omitempty"`
	Markers []Marker `json:"markers,omitempty"`
	Budget  string   `json:"budget,omitempty"` // "turn 3 of 40; sandbox runs 1 of 10"
}

type Point struct {
	Turn      int  `json:"turn"`
	Tokens    int  `json:"tokens"`
	Compacted bool `json:"compacted,omitempty"`
}

type Call struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Phase     string   `json:"phase"` // read, edit, test, submit, other
	Args      []Arg    `json:"args"`
	Result    string   `json:"result"`
	ResultLen int      `json:"result_len"`
	Cut       bool     `json:"cut,omitempty"` // Result was truncated by the recorder
	Failed    bool     `json:"failed,omitempty"`
	Diff      *Diff    `json:"diff,omitempty"`
	Sandbox   *Sandbox `json:"sandbox,omitempty"`
	Markers   []Marker `json:"markers,omitempty"`
}

type Arg struct {
	Key string `json:"key"`
	Val string `json:"val"`
}

type Marker struct {
	Kind string `json:"kind"` // commit_nudge, loop_warning, submit_reminder, syntax_warning, refused, no_tool_call
	Text string `json:"text"`
}

type Diff struct {
	Path  string     `json:"path"`
	Kind  string     `json:"kind"` // replace, lines, create
	Note  string     `json:"note,omitempty"`
	Lines []DiffLine `json:"lines"`
}

type DiffLine struct {
	Op   string `json:"op"` // "+", "-", " "
	Text string `json:"text"`
}

type Sandbox struct {
	Dir      string `json:"dir"` // attempt-N or python-N
	ExitCode *int   `json:"exit_code,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
	RunMS    int64  `json:"run_ms,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	CutOut   bool   `json:"stdout_cut,omitempty"`
	CutErr   bool   `json:"stderr_cut,omitempty"`
}

type Evidence struct {
	Verifier        string   `json:"verifier,omitempty"`
	RunID           string   `json:"run_id,omitempty"`
	Verdict         string   `json:"verdict"`
	Reasons         []string `json:"reasons,omitempty"`
	Required        int      `json:"required"`
	RequiredPassed  int      `json:"required_passed"`
	Passed          int      `json:"passed"`
	Failed          []string `json:"failed,omitempty"`
	MissingRequired []string `json:"missing_required,omitempty"`
	Hashes          []Arg    `json:"hashes,omitempty"`
	Sandbox         []Arg    `json:"sandbox,omitempty"`
}

type rawSummary struct {
	Task                 string         `json:"task"`
	Model                string         `json:"model"`
	Host                 string         `json:"host"`
	Verdict              string         `json:"verdict"`
	Reasons              []string       `json:"reasons"`
	Infra                string         `json:"infra_error"`
	Sampling             map[string]any `json:"sampling"`
	AgentClaimed         bool           `json:"agent_claimed_fixed"`
	LastOwnTest          string         `json:"last_own_test"`
	ClaimAgainstOwnTests bool           `json:"claim_against_own_tests"`
	Harness              string         `json:"harness"`
	StopReason           string         `json:"stop_reason"`
	Turns                int            `json:"turns"`
	TestRuns             int            `json:"test_runs"`
	PythonRuns           int            `json:"python_runs"`
	ToolCalls            map[string]int `json:"tool_calls"`
	CompactBatches       int            `json:"compact_batches"`
	Budget               map[string]int `json:"budget"`
	PromptTok            int            `json:"prompt_tokens"`
	OutputTok            int            `json:"completion_tokens"`
	WallMS               int64          `json:"wall_ms"`
	ServedBy             map[string]int `json:"served_by"`
	SelfTest             string         `json:"self_test"`
}

type rawEvidence struct {
	Verifier string `json:"verifier"`
	RunID    string `json:"run_id"`
	Task     struct {
		Issue         string   `json:"issue"`
		RequiredTests []string `json:"required_tests"`
	} `json:"task"`
	Inputs  map[string]any `json:"inputs"`
	Verdict string         `json:"verdict"`
	Reasons []string       `json:"reasons"`
	Tests   struct {
		Passed          []string `json:"passed"`
		Failed          []string `json:"failed"`
		MissingRequired []string `json:"missing_required"`
	} `json:"tests"`
	Sandbox map[string]any `json:"sandbox"`
	QueueMS int64          `json:"sandbox_queue_ms"`
}

// Load reads a run directory. Only the transcript is needed; every other file
// is optional and its absence is noted on the page. A directory with no
// transcript, or an empty one, gives a Run with Empty set.
func Load(dir string, o Options) (*Run, error) {
	o = o.norm()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("run directory %s: not a readable directory", dir)
	}
	r := &Run{Dir: dir, Task: filepath.Base(dir), Verdict: "UNKNOWN", Items: []Item{}, Estimate: []Point{}}
	note := func(f string, args ...any) { r.Notes = append(r.Notes, fmt.Sprintf(f, args...)) }

	var sum rawSummary
	if err := readJSON(filepath.Join(dir, "summary.json"), &sum); err != nil {
		note("summary.json missing or unreadable (%v): header figures are derived from the transcript", errBrief(err))
	} else {
		r.Task, r.Model, r.Host, r.Harness = sum.Task, sum.Model, sum.Host, sum.Harness
		r.Verdict, r.Reasons, r.Infra = sum.Verdict, sum.Reasons, sum.Infra
		r.Claimed, r.ClaimAgainstOwnTests, r.LastOwnTest = sum.AgentClaimed, sum.ClaimAgainstOwnTests, sum.LastOwnTest
		r.StopReason, r.Turns, r.TestRuns, r.PythonRuns = sum.StopReason, sum.Turns, sum.TestRuns, sum.PythonRuns
		r.PromptTok, r.OutputTok, r.WallMS, r.ServedBy = sum.PromptTok, sum.OutputTok, sum.WallMS, sum.ServedBy
		r.MaxTurns, r.MaxSandbox, r.CompactAbove = sum.Budget["max_turns"], sum.Budget["max_sandbox_runs"], sum.Budget["compact_above"]
		r.CompactBatches, r.SelfTest, r.ToolCounts = sum.CompactBatches, sum.SelfTest, sum.ToolCalls
		if v, ok := sum.Sampling["context_tokens"].(float64); ok {
			r.Window = int(v)
		}
		r.OverClaim = sum.AgentClaimed && sum.Verdict != "PASS"
	}

	if b, err := os.ReadFile(filepath.Join(dir, "final.patch")); err != nil {
		note("final.patch missing")
	} else {
		r.Patch = string(b)
	}
	if ev, issue := loadEvidence(filepath.Join(dir, "final", "evidence.json")); ev == nil {
		note("final/evidence.json missing or unreadable: no verification evidence to show")
	} else {
		r.Evidence = ev
		r.Issue = issue
	}
	if ev, issue := loadEvidence(filepath.Join(dir, "baseline", "evidence.json")); ev != nil {
		r.Baseline = ev
		if r.Issue == "" {
			r.Issue = issue
		}
	}

	msgs, bad, err := readTranscript(filepath.Join(dir, "transcript.jsonl"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		note("transcript.jsonl missing")
	case err != nil:
		note("transcript.jsonl unreadable: %v", err)
	}
	if bad > 0 {
		note("%d transcript line(s) were not valid JSON and were skipped", bad)
	}
	if len(msgs) == 0 {
		r.Empty = true
		return r, nil
	}
	buildTimeline(r, msgs, dir, o)
	return r, nil
}

func errBrief(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "not found"
	}
	return err.Error()
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func loadEvidence(path string) (*Evidence, string) {
	var re rawEvidence
	if readJSON(path, &re) != nil {
		return nil, ""
	}
	e := &Evidence{Verifier: re.Verifier, RunID: re.RunID, Verdict: re.Verdict, Reasons: re.Reasons,
		Passed: len(re.Tests.Passed), Failed: re.Tests.Failed, MissingRequired: re.Tests.MissingRequired}
	pass := map[string]bool{}
	for _, p := range re.Tests.Passed {
		pass[p] = true
	}
	e.Required = len(re.Task.RequiredTests)
	for _, t := range re.Task.RequiredTests {
		if pass[t] {
			e.RequiredPassed++
		}
	}
	for _, k := range sortedKeys(re.Inputs) {
		e.Hashes = append(e.Hashes, Arg{k, fmt.Sprint(re.Inputs[k])})
	}
	for _, k := range sortedKeys(re.Sandbox) {
		e.Sandbox = append(e.Sandbox, Arg{k, fmt.Sprint(re.Sandbox[k])})
	}
	if re.QueueMS > 0 {
		e.Sandbox = append(e.Sandbox, Arg{"sandbox_queue_ms", strconv.FormatInt(re.QueueMS, 10)})
	}
	return e, re.Task.Issue
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	// insertion sort: tiny maps
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j] < ks[j-1]; j-- {
			ks[j], ks[j-1] = ks[j-1], ks[j]
		}
	}
	return ks
}

// readTranscript parses one model.Message per line, skipping lines that are
// not valid JSON (and counting them). Lines can be arbitrarily long.
func readTranscript(path string) (msgs []model.Message, bad int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	for {
		line, rerr := br.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			var m model.Message
			if json.Unmarshal(t, &m) != nil {
				bad++
			} else {
				msgs = append(msgs, m)
			}
		}
		if rerr == io.EOF {
			return msgs, bad, nil
		}
		if rerr != nil {
			return msgs, bad, rerr
		}
	}
}

var (
	budgetRe = regexp.MustCompile(`\n?\[turn (\d+) of (\d+); sandbox runs (\d+) of (\d+)\]\s*$`)
)

// Intervention texts the harness appends to a tool result (internal/agent).
var interventions = []struct{ kind, prefix string }{
	{"commit_nudge", "\n\nYou have not changed any file yet, and "},
	{"loop_warning", "\n\nYou have sent this exact call "},
	{"loop_warning", "\n\nYou have made this exact call "},
	{"syntax_warning", "\n\nWARNING: after this edit the file no longer parses"},
}

const noToolCallNudge = "Use the tools. Call submit when you are done."

// splitResult separates the harness footer and appended interventions from the
// tool's own output.
func splitResult(res string) (body string, budget string, ms []Marker) {
	body = res
	if m := budgetRe.FindStringSubmatchIndex(body); m != nil {
		budget = fmt.Sprintf("turn %s of %s; sandbox runs %s of %s", body[m[2]:m[3]], body[m[4]:m[5]], body[m[6]:m[7]], body[m[8]:m[9]])
		body = body[:m[0]]
	}
	cut := len(body)
	for _, iv := range interventions {
		if i := strings.Index(body, iv.prefix); i >= 0 {
			ms = append(ms, Marker{iv.kind, strings.TrimSpace(body[i:])})
			if i < cut {
				cut = i
			}
			break
		}
	}
	body = body[:cut]
	switch {
	case strings.HasPrefix(body, "Not submitted yet: you have not run a reproduction"):
		ms = append(ms, Marker{"submit_reminder", body})
	case strings.HasPrefix(body, "refused:"):
		ms = append(ms, Marker{"refused", body})
	}
	return body, budget, ms
}

func phase(name string) string {
	switch name {
	case "read_file", "list_dir", "list_files", "search":
		return "read"
	case "replace_in_file", "replace_lines", "create_file":
		return "edit"
	case "run_tests", "run_python":
		return "test"
	case "submit":
		return "submit"
	}
	return "other"
}

func isFailure(res string) bool {
	return strings.HasPrefix(res, "error:") || strings.HasPrefix(res, "refused:")
}

func clip(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	s = s[:n]
	for len(s) > 0 && !validTail(s) { // do not cut a UTF-8 sequence in half
		s = s[:len(s)-1]
	}
	return s, true
}

func validTail(s string) bool {
	return strings.ToValidUTF8(s[max(0, len(s)-4):], "\x00") == s[max(0, len(s)-4):]
}

func buildTimeline(r *Run, msgs []model.Message, dir string, o Options) {
	results := map[string]model.Message{}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			results[m.ToolCallID] = m
		}
	}
	comp := &compactor{High: r.CompactAbove, Keep: 3}
	attempt := 0
	turn := 0
	cutAny := false
	firstUser := true
	for i, m := range msgs {
		switch m.Role {
		case "system":
		case "user":
			if firstUser {
				firstUser = false
				if r.Issue == "" {
					r.Issue = m.Content
				}
				continue
			}
			it := Item{Kind: "harness", Text: m.Content}
			if m.Content == noToolCallNudge {
				it.Markers = []Marker{{"no_tool_call", "The reply had no tool call; the harness told the model to use the tools."}}
			}
			r.Items = append(r.Items, it)
		case "assistant":
			turn++
			pt, compacted := comp.view(msgs[:i])
			r.Estimate = append(r.Estimate, Point{Turn: turn, Tokens: pt, Compacted: compacted})
			it := Item{Kind: "turn", N: turn, Text: m.Content}
			for _, tc := range m.ToolCalls {
				c := Call{ID: tc.ID, Name: tc.Function.Name, Phase: phase(tc.Function.Name)}
				var args map[string]any
				argErr := json.Unmarshal([]byte(tc.Function.Arguments), &args)
				if argErr != nil {
					c.Args = []Arg{{"arguments", tc.Function.Arguments}}
				} else {
					for _, k := range sortedKeys(args) {
						c.Args = append(c.Args, Arg{k, argString(args[k])})
					}
				}
				var res string
				if rm, ok := results[tc.ID]; ok {
					res = rm.Content
				} else {
					res = "(no result recorded: the run ended before this call was answered)"
				}
				body, budget, ms := splitResult(res)
				if budget != "" {
					it.Budget = budget
				}
				c.Markers = ms
				c.ResultLen = len(body)
				c.Result, c.Cut = clip(body, o.MaxResult)
				cutAny = cutAny || c.Cut
				c.Failed = isFailure(body)
				if c.Phase == "edit" && argErr == nil {
					c.Diff = editDiff(c.Name, args)
				}
				if c.Name == "run_tests" || c.Name == "run_python" {
					if !(c.Failed || strings.HasPrefix(body, "no changes yet") || strings.HasPrefix(body, "error:")) {
						attempt++
						c.Sandbox = loadSandbox(dir, c.Name, attempt, o)
					}
				}
				it.Calls = append(it.Calls, c)
			}
			r.Items = append(r.Items, it)
		}
	}
	if cutAny {
		r.Notes = append(r.Notes, fmt.Sprintf("tool results longer than %d characters were truncated in this page; the transcript on disk is complete", o.MaxResult))
	}
	if r.Turns == 0 {
		r.Turns = turn
	}
	if r.Model == "" {
		r.Model = "(unknown)"
	}
}

func argString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "null"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func loadSandbox(runDir, tool string, n int, o Options) *Sandbox {
	name := fmt.Sprintf("python-%d", n)
	if tool == "run_tests" {
		name = fmt.Sprintf("attempt-%d", n)
	}
	d := filepath.Join(runDir, name)
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return &Sandbox{Dir: name + " (not found)"}
	}
	sb := &Sandbox{Dir: name}
	var res struct {
		ExitCode *int  `json:"exit_code"`
		TimedOut bool  `json:"timed_out"`
		RunMS    int64 `json:"run_ms"`
	}
	if readJSON(filepath.Join(d, "result.json"), &res) == nil {
		sb.ExitCode, sb.TimedOut, sb.RunMS = res.ExitCode, res.TimedOut, res.RunMS
	}
	var ev rawEvidence
	if readJSON(filepath.Join(d, "evidence.json"), &ev) == nil {
		sb.Verdict = ev.Verdict
	}
	sb.Stdout, sb.CutOut = tailFile(filepath.Join(d, "stdout.log"), o)
	sb.Stderr, sb.CutErr = tailFile(filepath.Join(d, "stderr.log"), o)
	return sb
}

func tailFile(path string, o Options) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := strings.TrimRight(string(b), "\n")
	cut := false
	lines := strings.Split(s, "\n")
	if len(lines) > o.TailLines {
		lines, cut = lines[len(lines)-o.TailLines:], true
	}
	s = strings.Join(lines, "\n")
	if len(s) > o.TailBytes {
		s = strings.ToValidUTF8(s[len(s)-o.TailBytes:], "")
		cut = true
	}
	return s, cut
}

// editDiff renders an edit tool call's arguments as a diff. The transcript
// does not hold the file, so replace_lines shows only the new text.
func editDiff(name string, args map[string]any) *Diff {
	str := func(k string) string { s, _ := args[k].(string); return s }
	d := &Diff{Path: str("path")}
	switch name {
	case "replace_in_file":
		d.Kind = "replace"
		d.Lines = lineDiff(str("old_text"), str("new_text"))
	case "replace_lines":
		d.Kind = "lines"
		d.Note = fmt.Sprintf("lines %s to %s replaced (old text not recorded in the call)", argString(args["start_line"]), argString(args["end_line"]))
		for _, l := range splitLines(str("new_text")) {
			d.Lines = append(d.Lines, DiffLine{"+", l})
		}
	case "create_file":
		d.Kind = "create"
		d.Note = "new file"
		for _, l := range splitLines(str("content")) {
			d.Lines = append(d.Lines, DiffLine{"+", l})
		}
	default:
		return nil
	}
	return d
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lineDiff shows old as removed and new as added, with the lines the two share
// at either end as context.
func lineDiff(oldT, newT string) []DiffLine {
	a, b := splitLines(oldT), splitLines(newT)
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var out []DiffLine
	for _, l := range a[:p] {
		out = append(out, DiffLine{" ", l})
	}
	for _, l := range a[p : len(a)-s] {
		out = append(out, DiffLine{"-", l})
	}
	for _, l := range b[p : len(b)-s] {
		out = append(out, DiffLine{"+", l})
	}
	for _, l := range a[len(a)-s:] {
		out = append(out, DiffLine{" ", l})
	}
	return out
}

// compactor mirrors the agent's: when the conversation passes High, older tool
// outputs are shortened in one batch. view returns the estimated prompt size
// the model would have seen, and whether this call started a batch.
type compactor struct {
	High      int
	Keep      int
	shortened map[int]bool
}

func (c *compactor) view(msgs []model.Message) (int, bool) {
	if c.shortened == nil {
		c.shortened = map[int]bool{}
	}
	build := func() int {
		out := make([]model.Message, len(msgs))
		copy(out, msgs)
		for i := range out {
			if c.shortened[i] && len(out[i].Content) > 400 {
				out[i].Content = out[i].Content[:400] + "\n[... older output shortened by brokkr; call the tool again if you need it]"
			}
		}
		return model.EstimateTokens(out)
	}
	n := build()
	if c.High <= 0 || n <= c.High {
		return n, false
	}
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			seen++
			if seen > c.Keep {
				c.shortened[i] = true
			}
		}
	}
	return build(), true
}

type page struct {
	Title string
	CSS   template.CSS
	JS    template.JS
	Data  template.JS
}

// Render writes one self-contained page for the runs (one, or two to compare).
func Render(w io.Writer, runs ...*Run) error {
	if len(runs) == 0 {
		return errors.New("replay: no runs")
	}
	// json.Marshal escapes <, > and & as <, >, &, so no run text
	// can close the script element or open a comment inside it.
	b, err := json.Marshal(struct {
		Runs []*Run `json:"runs"`
	}{runs})
	if err != nil {
		return err
	}
	title := "Brokkr replay: " + runs[0].Task
	if len(runs) > 1 {
		title = "Brokkr replay: " + runs[0].Task + " vs " + runs[1].Task
	}
	t, err := template.New("page").Parse(pageTmpl)
	if err != nil {
		return err
	}
	return t.Execute(w, page{Title: title, CSS: template.CSS(styleCSS), JS: template.JS(appJS), Data: template.JS(b)})
}

// WriteFile loads runDir (and compareDir if set) and writes the page to out.
func WriteFile(runDir, compareDir, out string, o Options) error {
	a, err := Load(runDir, o)
	if err != nil {
		return err
	}
	runs := []*Run{a}
	if compareDir != "" {
		b, err := Load(compareDir, o)
		if err != nil {
			return err
		}
		runs = append(runs, b)
	}
	var buf bytes.Buffer
	if err := Render(&buf, runs...); err != nil {
		return err
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}
