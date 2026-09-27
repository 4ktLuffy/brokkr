package model

import (
	"encoding/json"
	"testing"
)

func TestContentStringOrParts(t *testing.T) {
	cases := map[string]string{
		`{"role":"assistant","content":"hi"}`: "hi",
		`{"role":"assistant","content":null}`: "",
		`{"role":"assistant"}`:                "",
		`{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`:                "ab",
		`{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"answer"}]}`: "answer",
	}
	for in, want := range cases {
		var m Message
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if m.Content != want || m.Role != "assistant" {
			t.Errorf("%s: content %q role %q, want %q", in, m.Content, m.Role, want)
		}
	}
	var m Message
	if err := json.Unmarshal([]byte(`{"content":42}`), &m); err == nil {
		t.Error("a number as content should be an error")
	}
}

// Tool calls survive the custom decoding, and encoding is unchanged.
func TestToolCallsAndEncoding(t *testing.T) {
	var m Message
	in := `{"role":"assistant","content":[],"tool_calls":[{"id":"1","type":"function","function":{"name":"f","arguments":"{}"}}]}`
	if err := json.Unmarshal([]byte(in), &m); err != nil || len(m.ToolCalls) != 1 || m.ToolCalls[0].Function.Name != "f" {
		t.Fatalf("m=%+v err=%v", m, err)
	}
	out, _ := json.Marshal(Message{Role: "user", Content: "x"})
	if string(out) != `{"role":"user","content":"x"}` {
		t.Errorf("encoding changed: %s", out)
	}
}
