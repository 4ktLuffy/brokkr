# Invalid run: the model was served with a truncated context

Kept as a record, not a result. Do not compare against it.

This run used `qwen2.5:7b` through Ollama's OpenAI-compatible endpoint, which
served it with a 4096-token context window (the model supports 32768). Ollama's
server log shows prompts cut to fit (`truncated = 1`) and repeated context shifts
during generation. Once a conversation outgrew the window, the model lost the
oldest messages, including the system prompt and the task. Two workdays runs hit
the 10-minute client timeout during such generations and were scored as agent
failures.

`scripts/probe-context.py` reproduces the truncation: a ~14K-token prompt is
reported as 2050 prompt tokens and the instruction at its start is lost.

What changed afterwards:
- `dev/ollama/qwen2.5-7b-16k.Modelfile` serves the same weights with a 16K window.
- The agent stops a run as an infra error, never an agent failure, when the
  conversation is estimated to exceed the window or the reported prompt size
  shrinks between turns.
- Model errors and timeouts are infra errors too, listed separately in the summary.
