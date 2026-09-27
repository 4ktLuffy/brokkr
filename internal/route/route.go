// Package route sends an agent's model calls to one of several OpenAI-compatible
// backends (local Ollama, a free tier behind tools/freetier_proxy.py, ...).
//
// Two decisions, kept apart:
//
//   - Per task (Pick): order the backends by their measured pass rate on the
//     task's repository (from results/*/runs.jsonl), skipping backends that
//     do not answer. The numbers are a prior for choosing, not an evaluation:
//     they pool harness versions, which the evaluation never does.
//   - Per call (Router.Chat): if the current backend is parked (free tier
//     spent), rate-limited or down, continue the same conversation on the next
//     backend whose context window holds it. A malformed reply is the model's
//     failure and is returned, never routed around.
//
// A run served by more than one backend is a mixed run. Its summary says so
// (served_by), and model comparisons must leave it out.
package route

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/4ktLuffy/brokkr/internal/model"
)

// Backend is one model endpoint.
type Backend struct {
	Name            string  `json:"name"`
	BaseURL         string  `json:"base_url"`
	Model           string  `json:"model"`
	APIKeyEnv       string  `json:"api_key_env,omitempty"` // read from the environment; never stored
	ContextTokens   int     `json:"context_tokens"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"top_p,omitempty"`
	PresencePenalty float64 `json:"presence_penalty,omitempty"`
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`
	MaxReplyTokens  int     `json:"max_reply_tokens,omitempty"`
	// StatsModel is the model name as recorded in runs.jsonl, when it
	// differs from Model.
	StatsModel string `json:"stats_model,omitempty"`
}

// Config is a routes file (dev/routes.json).
type Config struct {
	Backends []Backend `json:"backends"`
	// Objective: "pass_rate" (default) or "pass_per_hour", which also
	// weighs how long a backend's runs take.
	Objective string `json:"objective,omitempty"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(c.Backends) == 0 {
		return nil, fmt.Errorf("%s: no backends", path)
	}
	seen := map[string]bool{}
	for _, b := range c.Backends {
		if b.Name == "" || b.BaseURL == "" || b.Model == "" || b.ContextTokens <= 0 {
			return nil, fmt.Errorf("%s: backend %q needs name, base_url, model and context_tokens", path, b.Name)
		}
		if seen[b.Name] {
			return nil, fmt.Errorf("%s: backend %q twice", path, b.Name)
		}
		seen[b.Name] = true
	}
	return &c, nil
}

// Client builds the backend's model client.
func (b Backend) Client() *model.Client {
	c := &model.Client{BaseURL: b.BaseURL, Model: b.Model, Temperature: b.Temperature, TopP: b.TopP,
		PresencePenalty: b.PresencePenalty, ReasoningEffort: b.ReasoningEffort, MaxTokens: b.MaxReplyTokens}
	if c.MaxTokens == 0 {
		c.MaxTokens = 2048
	}
	if b.APIKeyEnv != "" {
		c.APIKey = os.Getenv(b.APIKeyEnv)
	}
	return c
}

// Up asks the backend for its model list. A proxy or server that does not
// answer within the timeout is treated as down for this run.
func Up(ctx context.Context, b Backend, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(b.BaseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	if b.APIKeyEnv != "" {
		if k := os.Getenv(b.APIKeyEnv); k != "" {
			req.Header.Set("Authorization", "Bearer "+k)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// Any answer below 500 means the server is there: a proxy need not
	// serve /models (tools/freetier_proxy.py does not).
	if resp.StatusCode >= 500 {
		return fmt.Errorf("GET /models: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Switch records one fallback.
type Switch struct {
	Turn   int    `json:"turn"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// Router serves Chat from an ordered list of backends.
type Router struct {
	backends []Backend
	clients  []model.Client
	cur      int
	down     map[int]time.Time // backend index -> usable again after
	owner    map[string]int    // tool call id -> backend that produced it
	turn     int
	now      func() time.Time

	ServedBy map[string]int
	Switches []Switch
}

// New routes over backends in the given order (normally Pick's).
func New(backends []Backend) *Router {
	r := &Router{backends: backends, down: map[int]time.Time{}, owner: map[string]int{}, now: time.Now, ServedBy: map[string]int{}}
	for _, b := range backends {
		r.clients = append(r.clients, *b.Client())
	}
	return r
}

// Primary is the backend a run starts on.
func (r *Router) Primary() Backend { return r.backends[0] }

// MaxContext is the largest window among the backends: the conversation can
// grow to it, moving to a bigger backend as it outgrows a smaller one.
func (r *Router) MaxContext() int {
	m := 0
	for _, b := range r.backends {
		m = max(m, b.ContextTokens)
	}
	return m
}

// Current is the backend serving now.
func (r *Router) Current() Backend { return r.backends[r.cur] }

// unavailable says whether err means "this backend cannot serve now" (try
// another) and until when.
func (r *Router) unavailable(err error) (bool, time.Time, string) {
	var parked *model.ErrBudgetParked
	var bad *model.ErrMalformedReply
	var he *model.ErrHTTP
	switch {
	case errors.As(err, &bad):
		return false, time.Time{}, ""
	case errors.As(err, &parked):
		until, perr := time.Parse(time.RFC3339, parked.ResumeAt)
		if perr != nil {
			until = r.now().Add(time.Hour)
		}
		return true, until, "parked until " + parked.ResumeAt
	case errors.As(err, &he):
		if he.Status == http.StatusTooManyRequests || he.Status >= 500 {
			return true, r.now().Add(2 * time.Minute), fmt.Sprintf("HTTP %d", he.Status)
		}
		return false, time.Time{}, ""
	case errors.Is(err, context.Canceled):
		return false, time.Time{}, ""
	default:
		// Transport errors: refused connection, timeout, reset.
		return true, r.now().Add(5 * time.Minute), "unreachable: " + firstLine(err.Error())
	}
}

// Chat implements the agent's model interface.
func (r *Router) Chat(ctx context.Context, msgs []model.Message, tools []model.Tool) (model.Message, model.Usage, error) {
	r.turn++
	need := model.EstimateTokens(msgs)
	var lastErr, parkedErr error
	for tried := 0; tried < len(r.backends); tried++ {
		i := r.next(need)
		if i < 0 {
			break
		}
		if i != r.cur {
			reason := "down"
			switch {
			case lastErr != nil:
				_, _, reason = r.unavailable(lastErr)
			case need > r.backends[r.cur].ContextTokens:
				reason = fmt.Sprintf("conversation (~%d tokens) outgrew the %d-token window", need, r.backends[r.cur].ContextTokens)
			}
			r.Switches = append(r.Switches, Switch{Turn: r.turn, From: r.backends[r.cur].Name, To: r.backends[i].Name, Reason: reason})
			r.cur = i
		}
		reply, usage, err := r.clients[i].Chat(ctx, r.forBackend(msgs, i), tools)
		if err == nil {
			r.ServedBy[r.backends[i].Name]++
			for _, c := range reply.ToolCalls {
				r.owner[c.ID] = i
			}
			return reply, usage, nil
		}
		ok, until, _ := r.unavailable(err)
		if !ok {
			return reply, usage, err
		}
		r.down[i] = until
		lastErr = err
		if errors.As(err, new(*model.ErrBudgetParked)) {
			parkedErr = err
		}
	}
	// Nothing left. If any backend was parked, say that: a parked batch stops
	// and resumes later (exit 4), where an unreachable one is an infra error.
	if parkedErr != nil {
		return model.Message{}, model.Usage{}, parkedErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no backend can hold a %d-token prompt", need)
	}
	return model.Message{}, model.Usage{}, lastErr
}

// next is the current backend if usable, else the first usable one in order
// whose window holds the conversation.
func (r *Router) next(need int) int {
	usable := func(i int) bool {
		return r.now().After(r.down[i]) && need <= r.backends[i].ContextTokens
	}
	if usable(r.cur) {
		return r.cur
	}
	for i := range r.backends {
		if usable(i) {
			return i
		}
	}
	return -1
}

// forBackend rewrites tool-call ids that another backend produced into the
// 9-character alphanumeric form every provider accepts (Mistral rejects
// anything else). A run on one backend is sent unchanged.
func (r *Router) forBackend(msgs []model.Message, i int) []model.Message {
	foreign := func(id string) bool { o, ok := r.owner[id]; return ok && o != i }
	changed := false
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			changed = changed || foreign(c.ID)
		}
	}
	if !changed {
		return msgs
	}
	out := make([]model.Message, len(msgs))
	for k, m := range msgs {
		if len(m.ToolCalls) > 0 {
			m.ToolCalls = append([]model.ToolCall(nil), m.ToolCalls...)
			for j := range m.ToolCalls {
				if foreign(m.ToolCalls[j].ID) {
					m.ToolCalls[j].ID = shortID(m.ToolCalls[j].ID)
				}
			}
		}
		if m.ToolCallID != "" && foreign(m.ToolCallID) {
			m.ToolCallID = shortID(m.ToolCallID)
		}
		out[k] = m
	}
	return out
}

func shortID(id string) string {
	const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	h := sha256.Sum256([]byte(id))
	b := make([]byte, 9)
	for k := range b {
		b[k] = alnum[int(h[k])%len(alnum)]
	}
	return string(b)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
