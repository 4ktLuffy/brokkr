// Package model is a minimal client for OpenAI-compatible chat completions
// with tool calling. Ollama, vLLM, llama.cpp and most hosted providers speak it.
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// UnmarshalJSON accepts content as a string or as a list of parts. Mistral
// sometimes replies with [{"type":"text","text":...}, ...]; only text parts
// are kept (a "thinking" part is not the answer). null is "".
func (m *Message) UnmarshalJSON(b []byte) error {
	type plain Message
	var raw struct {
		plain
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*m = Message(raw.plain)
	c := bytes.TrimSpace(raw.Content)
	switch {
	case len(c) == 0 || string(c) == "null":
		m.Content = ""
	case c[0] == '"':
		return json.Unmarshal(c, &m.Content)
	case c[0] == '[':
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(c, &parts); err != nil {
			return fmt.Errorf("content parts: %w", err)
		}
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				sb.WriteString(p.Text)
			}
		}
		m.Content = sb.String()
	default:
		return fmt.Errorf("content is neither a string nor a list: %.60s", c)
	}
	return nil
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ErrBudgetParked is returned when a metered free tier has no allowance left
// (tools/freetier_proxy.py answers HTTP 429 with error type "budget_parked").
// Retrying before ResumeAt only spends nothing to learn nothing.
type ErrBudgetParked struct {
	ResumeAt string
	Message  string
}

func (e *ErrBudgetParked) Error() string {
	return "budget parked until " + e.ResumeAt + ": " + e.Message
}

// ErrMalformedReply is a server-side rejection of what the model generated,
// such as a tool call the server could not parse. It is the model's failure,
// not the infrastructure's, and is scored as such after retries.
type ErrMalformedReply struct{ Detail string }

func (e *ErrMalformedReply) Error() string { return "malformed model reply: " + e.Detail }

// ErrHTTP is any other non-200 answer. The router uses the status to tell a
// backend that is down or rate-limited (429, 5xx: try another) from a request
// the backend refused (other 4xx: trying elsewhere would hide a bug).
type ErrHTTP struct {
	Model  string
	Status int
	Body   string
}

func (e *ErrHTTP) Error() string {
	return fmt.Sprintf("model %s: HTTP %d: %.300s", e.Model, e.Status, e.Body)
}

// EstimateTokens over-estimates a conversation's prompt size: about three
// characters per token, plus a fixed allowance for the tool schemas and chat
// template. Erring high stops a run early, which is reported; erring low would
// let a truncated run be scored, which is not.
func EstimateTokens(msgs []Message) int {
	chars := 0
	for _, m := range msgs {
		chars += len(m.Content) + 16
		for _, c := range m.ToolCalls {
			chars += len(c.Function.Name) + len(c.Function.Arguments) + 16
		}
	}
	return chars/3 + 800
}

// malformedMarkers identify server errors caused by the model's own output.
// Ollama reports an unparseable tool call as HTTP 500 with the parser error.
var malformedMarkers = []string{"XML syntax error", "error parsing tool call", "failed to parse", "invalid character"}

type Client struct {
	BaseURL     string // e.g. http://host.lima.internal:11434/v1
	Model       string
	APIKey      string // optional; never logged
	Temperature float64
	TopP        float64 // 0 omits it
	// PresencePenalty 0 omits it. Qwen recommends 1.5 for agentic use.
	PresencePenalty float64
	// ReasoningEffort is sent when set: "none" turns a thinking model's
	// thinking off (Ollama maps it for qwen3.5).
	ReasoningEffort string
	MaxTokens       int // cap per reply, so a degenerate generation cannot run on
	HTTP            *http.Client
}

func (c *Client) Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, Usage, error) {
	req := map[string]any{
		"model":       c.Model,
		"messages":    msgs,
		"tools":       tools,
		"temperature": c.Temperature,
		"max_tokens":  c.MaxTokens,
		"stream":      false,
	}
	if c.TopP > 0 {
		req["top_p"] = c.TopP
	}
	if c.PresencePenalty != 0 {
		req["presence_penalty"] = c.PresencePenalty
	}
	if c.ReasoningEffort != "" {
		req["reasoning_effort"] = c.ReasoningEffort
	}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		var parked struct {
			Error struct {
				Type     string `json:"type"`
				Message  string `json:"message"`
				ResumeAt string `json:"resume_at"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &parked) == nil && parked.Error.Type == "budget_parked" {
			return Message{}, Usage{}, &ErrBudgetParked{ResumeAt: parked.Error.ResumeAt, Message: parked.Error.Message}
		}
	}
	if resp.StatusCode >= 500 {
		for _, m := range malformedMarkers {
			if bytes.Contains(raw, []byte(m)) {
				return Message{}, Usage{}, &ErrMalformedReply{Detail: fmt.Sprintf("HTTP %d: %.300s", resp.StatusCode, raw)}
			}
		}
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, Usage{}, &ErrHTTP{Model: c.Model, Status: resp.StatusCode, Body: string(raw)}
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, Usage{}, fmt.Errorf("model %s: bad response: %w", c.Model, err)
	}
	if len(out.Choices) == 0 {
		return Message{}, out.Usage, fmt.Errorf("model %s: no choices", c.Model)
	}
	return out.Choices[0].Message, out.Usage, nil
}
