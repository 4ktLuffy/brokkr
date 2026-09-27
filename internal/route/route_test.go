package route

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/4ktLuffy/brokkr/internal/model"
)

// fake is a backend answering with a fixed status and body, recording the
// requests it saw.
type fake struct {
	srv  *httptest.Server
	seen []map[string]any
}

func newFake(t *testing.T, status int, body string) *fake {
	f := &fake{}
	n := 0
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(raw, &req)
		f.seen = append(f.seen, req)
		w.WriteHeader(status)
		n++
		if strings.Contains(body, "%d") {
			fmt.Fprintf(w, body, n)
		} else {
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// ok answers a tool call with a backend-specific id format.
func ok(t *testing.T, idPrefix string) *fake {
	return newFake(t, 200, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"`+idPrefix+`%d","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`)
}

func be(name string, f *fake, ctx int) Backend {
	return Backend{Name: name, BaseURL: f.srv.URL, Model: name, ContextTokens: ctx}
}

var hello = []model.Message{{Role: "user", Content: "hi"}}

func TestFallsBackWhenParked(t *testing.T) {
	parked := newFake(t, 429, `{"error":{"type":"budget_parked","message":"spent","resume_at":"2099-01-01T00:00:00Z"}}`)
	local := ok(t, "call_")
	r := New([]Backend{be("codestral", parked, 256000), be("qwen", local, 32768)})
	if _, _, err := r.Chat(context.Background(), hello, nil); err != nil {
		t.Fatal(err)
	}
	if r.ServedBy["qwen"] != 1 || len(r.Switches) != 1 || !strings.Contains(r.Switches[0].Reason, "parked") {
		t.Fatalf("served %v switches %+v", r.ServedBy, r.Switches)
	}
	// Parked until 2099: the next turn goes straight to qwen.
	r.Chat(context.Background(), hello, nil)
	if len(parked.seen) != 1 || r.ServedBy["qwen"] != 2 {
		t.Fatalf("parked backend asked %d times; served %v", len(parked.seen), r.ServedBy)
	}
}

func TestFallsBackOn5xxAndUnreachable(t *testing.T) {
	dead := httptest.NewServer(nil)
	dead.Close()
	five := newFake(t, 503, `busy %d`)
	local := ok(t, "call_")
	r := New([]Backend{{Name: "gone", BaseURL: dead.URL, Model: "x", ContextTokens: 1000}, be("busy", five, 1000), be("qwen", local, 1000)})
	if _, _, err := r.Chat(context.Background(), hello, nil); err != nil {
		t.Fatal(err)
	}
	if r.ServedBy["qwen"] != 1 || len(r.Switches) != 2 {
		t.Fatalf("served %v switches %+v", r.ServedBy, r.Switches)
	}
}

// Negative controls: the model's own failures and refused requests are not
// routed around.
func TestNoFallbackOnMalformedOr4xx(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
	}{
		{"malformed", 500, `error parsing tool call %d`},
		{"bad request", 400, `context too long %d`},
	} {
		first := newFake(t, c.status, c.body)
		second := ok(t, "x")
		r := New([]Backend{be("a", first, 1000), be("b", second, 1000)})
		if _, _, err := r.Chat(context.Background(), hello, nil); err == nil {
			t.Errorf("%s: no error", c.name)
		}
		if len(second.seen) != 0 {
			t.Errorf("%s: fell back to b", c.name)
		}
	}
}

func TestNeverFallsBackIntoATooSmallWindow(t *testing.T) {
	parked := newFake(t, 429, `{"error":{"type":"budget_parked","resume_at":"2099-01-01T00:00:00Z"}}`)
	small := ok(t, "x")
	r := New([]Backend{be("big", parked, 256000), be("small", small, 1000)})
	long := []model.Message{{Role: "user", Content: strings.Repeat("x", 30000)}} // ~10K tokens
	_, _, err := r.Chat(context.Background(), long, nil)
	var p *model.ErrBudgetParked
	if !errorsAs(err, &p) || len(small.seen) != 0 {
		t.Fatalf("err %v, small asked %d times", err, len(small.seen))
	}
}

// A conversation begun on Ollama (ids like call_1) continues on a backend
// that accepts only 9-character alphanumeric ids.
func TestForeignToolCallIDsAreRewritten(t *testing.T) {
	ollama := ok(t, "call_")
	r := New([]Backend{be("ollama", ollama, 1000), be("mistral", ok(t, "abcdefgh"), 1000)})
	reply, _, err := r.Chat(context.Background(), hello, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgs := append(hello, reply, model.Message{Role: "tool", Content: "a/", ToolCallID: reply.ToolCalls[0].ID, Name: "list_dir"})
	// Same backend: sent unchanged.
	if got := r.forBackend(msgs, 0); got[1].ToolCalls[0].ID != "call_1" || got[2].ToolCallID != "call_1" {
		t.Fatalf("same-backend ids changed: %+v", got)
	}
	got := r.forBackend(msgs, 1)
	id := got[1].ToolCalls[0].ID
	if !regexp.MustCompile(`^[a-zA-Z0-9]{9}$`).MatchString(id) || got[2].ToolCallID != id {
		t.Fatalf("rewritten ids %q / %q", id, got[2].ToolCallID)
	}
	if msgs[1].ToolCalls[0].ID != "call_1" {
		t.Fatal("the caller's messages were modified")
	}
}

func TestPickUsesRepoRecordAndSkipsDown(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "r1"), 0o755)
	var rows []string
	add := func(m, task, v string, n int) {
		for k := 0; k < n; k++ {
			rows = append(rows, fmt.Sprintf(`{"task":%q,"model":%q,"verdict":%q,"wall_ms":60000}`, task, m, v))
		}
	}
	add("codestral", "django__django-1", "PASS", 2)
	add("codestral", "django__django-1", "FAIL", 8)
	add("qwen", "django__django-2", "PASS", 5)
	add("qwen", "django__django-2", "FAIL", 5)
	add("qwen", "django__django-3", "ERROR", 50)                                                                 // unscored: ignored
	rows = append(rows, `{"task":"django__django-4","model":"qwen","verdict":"PASS","served_by":{"a":1,"b":1}}`) // mixed: ignored
	os.WriteFile(filepath.Join(dir, "r1", "runs.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644)
	st, n, err := LoadStats(filepath.Join(dir, "*", "runs.jsonl"))
	if err != nil || n != 20 {
		t.Fatalf("rows %d err %v", n, err)
	}
	cfg := &Config{Backends: []Backend{{Name: "c", Model: "codestral"}, {Name: "q", Model: "qwen"}, {Name: "new", Model: "untried"}}}
	names := func(r []Ranked) (s []string) {
		for _, x := range r {
			s = append(s, x.Backend.Name)
		}
		return
	}
	// Django: qwen 5/10 (est. 0.5), untried 0.5, codestral 2/10 (0.2). The tie
	// keeps config order.
	if got := names(Pick(cfg, st, "django/django", nil)); strings.Join(got, ",") != "q,new,c" {
		t.Fatalf("pick = %v", got)
	}
	if got := Order(Pick(cfg, st, "django/django", map[string]string{"q": "down"})); len(got) != 2 || got[0].Name != "new" {
		t.Fatalf("order with q down = %+v", got)
	}
	// No record on sympy: each backend's general record decides, so
	// codestral's 2/10 elsewhere puts it last, not first.
	if got := names(Pick(cfg, st, "sympy/sympy", nil)); strings.Join(got, ",") != "q,new,c" {
		t.Fatalf("pick sympy = %v", got)
	}
}

func TestRepoOf(t *testing.T) {
	for in, want := range map[string]string{"django__django-11099": "django/django", "pydantic__pydantic-13754-live": "pydantic/pydantic", "calc-mean-off-by-one": "toy"} {
		if got := RepoOf(in); got != want {
			t.Errorf("RepoOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func TestAllDownButOneParkedReportsParked(t *testing.T) {
	parked := newFake(t, 429, `{"error":{"type":"budget_parked","resume_at":"2099-01-01T00:00:00Z"}}`)
	dead := httptest.NewServer(nil)
	dead.Close()
	r := New([]Backend{be("codestral", parked, 1000), {Name: "gone", BaseURL: dead.URL, Model: "x", ContextTokens: 1000}})
	_, _, err := r.Chat(context.Background(), hello, nil)
	var p *model.ErrBudgetParked
	if !errorsAs(err, &p) {
		t.Fatalf("err = %v, want parked", err)
	}
}

// Start on the small local model; when the conversation outgrows its window,
// continue on the big one. A conversation that fits stays local.
func TestEscalatesWhenTheConversationOutgrowsTheWindow(t *testing.T) {
	local, big := ok(t, "call_"), ok(t, "abcdefgh")
	r := New([]Backend{be("qwen", local, 32768), be("codestral", big, 250000)})
	if r.MaxContext() != 250000 {
		t.Fatalf("MaxContext = %d", r.MaxContext())
	}
	r.Chat(context.Background(), hello, nil)
	long := []model.Message{{Role: "user", Content: strings.Repeat("x", 120000)}} // ~40K tokens
	if _, _, err := r.Chat(context.Background(), long, nil); err != nil {
		t.Fatal(err)
	}
	if r.ServedBy["qwen"] != 1 || r.ServedBy["codestral"] != 1 || len(r.Switches) != 1 || !strings.Contains(r.Switches[0].Reason, "outgrew") {
		t.Fatalf("served %v switches %+v", r.ServedBy, r.Switches)
	}
}

func TestUpAcceptsAServerWithoutModelsEndpoint(t *testing.T) {
	notFound := newFake(t, 404, `no such route`)
	if err := Up(context.Background(), be("proxy", notFound, 1), time.Second); err != nil {
		t.Fatalf("404 server reported down: %v", err)
	}
	dead := httptest.NewServer(nil)
	dead.Close()
	if err := Up(context.Background(), Backend{BaseURL: dead.URL}, time.Second); err == nil {
		t.Fatal("closed server reported up")
	}
}
