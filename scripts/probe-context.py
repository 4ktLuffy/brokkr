#!/usr/bin/env python3
"""Does the model server silently truncate long prompts?

Sends a ~14K-token prompt whose instruction is at the start, to each model
named on the command line, via the OpenAI-compatible endpoint. A server that
truncates reports a prompt_tokens far below the prompt's real size and the
model loses the instruction. Observed on the reference machine (Ollama 0.32.9):

    qwen2.5:7b             prompt_tokens = 2050   reply: 'Based on the information provided'
    brokkr-qwen2.5-7b-16k  prompt_tokens = 13921  reply: 'alpha'
    qwen3.5:9b             prompt_tokens = 2050   (cut)
    brokkr-qwen3.5-9b-32k  prompt_tokens = 13923  reply: 'alpha'

    scripts/probe-context.py qwen2.5:7b brokkr-qwen2.5-7b-16k
"""
import json
import os
import sys
import urllib.request

url = os.environ.get("BROKKR_MODEL_URL", "http://localhost:11434/v1").rstrip("/") + "/chat/completions"
big = " ".join(f"word{i}" for i in range(3000))
for model in sys.argv[1:]:
    # reasoning_effort=none: a thinking model would otherwise spend the 5 reply
    # tokens thinking and answer '', which reads as truncation when it is not.
    body = json.dumps({"model": model, "max_tokens": 5, "temperature": 0, "reasoning_effort": "none", "messages": [
        {"role": "system", "content": "SECRET=alpha. Reply with the SECRET value only."},
        {"role": "user", "content": big + "\nWhat is the SECRET value?"}]}).encode()
    r = json.load(urllib.request.urlopen(urllib.request.Request(
        url, body, {"Content-Type": "application/json"}), timeout=300))
    reply = r["choices"][0]["message"]["content"] or ""
    n = r["usage"]["prompt_tokens"]
    # The prompt is about 13.9K tokens. A count well below that means the server
    # dropped part of it; a full count with a wrong answer is a model problem.
    if n < 13000:
        verdict = "TRUNCATED (prompt cut)"
    elif "alpha" in reply:
        verdict = "OK"
    else:
        verdict = "full prompt received, but wrong answer"
    print(f"{model:24} prompt_tokens = {n:<6} reply: {reply!r}  -> {verdict}")
