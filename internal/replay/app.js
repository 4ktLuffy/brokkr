"use strict";
// Builds the whole page from the embedded JSON with textContent and
// createElement only. Nothing from a run is ever parsed as HTML.
(function () {
  var data;
  try { data = JSON.parse(document.getElementById("run-data").textContent); }
  catch (e) { document.getElementById("app").textContent = "The embedded run data could not be read: " + e.message; return; }
  var runs = data.runs || [];
  var app = document.getElementById("app");
  var SVGNS = "http://www.w3.org/2000/svg";
  var PHASE = { read: "reading", edit: "editing", test: "testing", submit: "submit", other: "other" };
  var MARK = { commit_nudge: "commit nudge", loop_warning: "loop warning", submit_reminder: "submit reminder",
    syntax_warning: "syntax warning", refused: "refused", no_tool_call: "no tool call" };

  function el(tag, cls, text, kids) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null && text !== "") e.textContent = text;
    (kids || []).forEach(function (k) { if (k) e.appendChild(k); });
    return e;
  }
  function sv(tag, attrs, text) {
    var e = document.createElementNS(SVGNS, tag);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (text != null) e.textContent = text;
    return e;
  }
  function fmtMS(ms) {
    if (!ms) return "0 s";
    var s = ms / 1000;
    if (s < 90) return s.toFixed(1) + " s";
    var m = Math.floor(s / 60);
    return m + " min " + Math.round(s - m * 60) + " s";
  }
  function fmtN(n) { return (n || 0).toLocaleString("en-US"); }
  function kv(label, value) { return el("div", "", "", [el("span", "", label), el("b", "", value)]); }
  function pre(text) { return el("pre", "", text); }

  function details(label, node) {
    var d = el("details"); d.appendChild(el("summary", "", label)); d.appendChild(node); return d;
  }
  // Long text: the head, with a button that reveals the rest.
  function longText(text, limit, cutNote) {
    var wrap = el("div");
    var p = pre(text.length > limit ? text.slice(0, limit) : text);
    wrap.appendChild(p);
    if (text.length > limit) {
      var b = el("button", "more", "show more (" + fmtN(text.length - limit) + " more characters)");
      b.type = "button";
      var open = false;
      b.addEventListener("click", function () {
        open = !open;
        p.textContent = open ? text : text.slice(0, limit);
        b.textContent = open ? "show less" : "show more (" + fmtN(text.length - limit) + " more characters)";
      });
      wrap.appendChild(b);
    }
    if (cutNote) wrap.appendChild(el("div", "hint", cutNote));
    return wrap;
  }
  function diffBox(lines, path, note) {
    var box = el("div", "diff");
    if (path) box.appendChild(el("div", "ph", path + (note ? "  (" + note + ")" : "")));
    lines.forEach(function (l) {
      box.appendChild(el("div", l.op === "+" ? "p" : l.op === "-" ? "m" : "", (l.op === " " ? "  " : l.op + " ") + l.text));
    });
    if (!lines.length) box.appendChild(el("div", "h", "(no lines)"));
    return box;
  }
  function patchBox(text) {
    var box = el("div", "diff");
    text.split("\n").forEach(function (l) {
      var c = "";
      if (l.indexOf("+++") === 0 || l.indexOf("---") === 0 || l.indexOf("diff ") === 0) c = "ph";
      else if (l.indexOf("@@") === 0) c = "h";
      else if (l[0] === "+") c = "p";
      else if (l[0] === "-") c = "m";
      box.appendChild(el("div", c, l === "" ? " " : l));
    });
    return box;
  }
  function markers(ms) {
    if (!ms || !ms.length) return null;
    var w = el("div");
    ms.forEach(function (m) {
      var s = el("span", "mk " + m.kind, "harness: " + (MARK[m.kind] || m.kind));
      s.title = m.text;
      w.appendChild(s);
    });
    ms.forEach(function (m) { if (m.text) w.appendChild(el("div", "text hint", m.text)); });
    return w;
  }

  function renderCall(c) {
    var d = el("div", "call " + c.phase + (c.failed ? " failed" : ""));
    d.appendChild(el("div", "", "", [el("span", "cname", c.name)]));
    var shown = c.diff ? c.args.filter(function (a) { return a.key === "path" || a.key === "start_line" || a.key === "end_line"; }) : c.args;
    if (shown.length) {
      var a = el("div", "args");
      shown.forEach(function (x) {
        var row = el("div");
        row.appendChild(el("span", "k", x.key + ": "));
        if (x.val.length > 300 || x.val.indexOf("\n") >= 0) {
          row.appendChild(longText(x.val, 600));
        } else row.appendChild(document.createTextNode(x.val));
        a.appendChild(row);
      });
      d.appendChild(a);
    }
    if (c.diff) d.appendChild(diffBox(c.diff.lines, c.diff.path, c.diff.note));
    if (c.sandbox) {
      var s = c.sandbox, bits = [];
      bits.push(s.dir);
      if (s.exit_code != null) bits.push("exit " + s.exit_code);
      if (s.timed_out) bits.push("TIMED OUT");
      if (s.verdict) bits.push(s.verdict);
      if (s.run_ms) bits.push(fmtMS(s.run_ms));
      var sb = el("div", "sb", "sandbox: " + bits.join(" · "));
      d.appendChild(sb);
      if (s.stdout) d.appendChild(details("stdout tail" + (s.stdout_cut ? " (cut)" : ""), pre(s.stdout)));
      if (s.stderr) d.appendChild(details("stderr tail" + (s.stderr_cut ? " (cut)" : ""), pre(s.stderr)));
    }
    var mk = markers(c.markers); if (mk) d.appendChild(mk);
    var label = "result" + (c.failed ? " (error)" : "") + " · " + fmtN(c.result_len) + " chars";
    var cutNote = c.cut ? "Truncated by the recorder at " + fmtN(c.result.length) + " of " + fmtN(c.result_len) + " characters; the transcript on disk has the rest." : "";
    d.appendChild(details(label, longText(c.result || "(empty)", 1200, cutNote)));
    return d;
  }

  function itemText(it) {
    var t = [it.text || ""];
    (it.calls || []).forEach(function (c) {
      t.push(c.name);
      c.args.forEach(function (a) { t.push(a.key + " " + a.val); });
      t.push(c.result || "");
      if (c.sandbox) t.push((c.sandbox.stdout || "") + " " + (c.sandbox.stderr || ""));
      (c.markers || []).forEach(function (m) { t.push(m.kind + " " + m.text); });
    });
    (it.markers || []).forEach(function (m) { t.push(m.kind + " " + m.text); });
    return t.join("\n").toLowerCase();
  }

  function renderTimeline(run, col) {
    var box = el("div");
    run.items.forEach(function (it) {
      var d = el("div", "turn" + (it.kind === "harness" ? " harness" : ""));
      d.dataset.search = itemText(it);
      d.tabIndex = -1;
      if (it.kind === "harness") {
        d.appendChild(el("div", "thead", "", [el("b", "", "Harness message to the model")]));
        var m = markers(it.markers);
        if (m) d.appendChild(m);
        d.appendChild(el("div", "text", it.text));
      } else {
        var names = (it.calls || []).map(function (c) { return c.name; }).join(", ");
        var head = el("div", "thead", "", [el("b", "", "Turn " + it.n), el("span", "", names || "no tool call")]);
        if (it.budget) head.appendChild(el("span", "", "[" + it.budget + "]"));
        d.appendChild(head);
        if (it.text) d.appendChild(el("div", "text", it.text));
        (it.calls || []).forEach(function (c) { d.appendChild(renderCall(c)); });
      }
      d.addEventListener("click", function () { setCur(col, d); });
      col.turns.push(d);
      box.appendChild(d);
    });
    return box;
  }

  var COLORS = { read: "var(--read)", edit: "var(--edit)", test: "var(--test)", submit: "var(--submit)", other: "var(--other)" };

  function renderStrip(run) {
    var cells = [];
    run.items.forEach(function (it) {
      if (it.kind !== "turn") return;
      if (!it.calls || !it.calls.length) cells.push({ n: it.n, phase: "other", label: "no tool call" });
      (it.calls || []).forEach(function (c) { cells.push({ n: it.n, phase: c.phase, label: c.name }); });
    });
    var w = 640, h = 44, cw = Math.min(24, (w - 20) / Math.max(cells.length, 1));
    var svg = sv("svg", { viewBox: "0 0 " + w + " " + h, role: "img", "aria-label": "Tool phases over the run" });
    var fixedEdit = -1;
    cells.forEach(function (c, i) {
      var r = sv("rect", { x: 10 + i * cw, y: 6, width: Math.max(cw - 1, 1), height: 22, rx: 2, fill: COLORS[c.phase] });
      r.appendChild(sv("title", {}, "turn " + c.n + ": " + c.label));
      svg.appendChild(r);
      if (c.phase === "edit" && fixedEdit < 0) fixedEdit = i;
    });
    if (fixedEdit >= 0) svg.appendChild(sv("text", { x: 10 + fixedEdit * cw, y: 40 }, "first edit"));
    else if (cells.length) svg.appendChild(sv("text", { x: 10, y: 40 }, "no edit in this run"));
    var wrap = el("div");
    wrap.appendChild(svg);
    var lg = el("div", "legend");
    Object.keys(COLORS).forEach(function (k) {
      var i = el("i"); i.style.background = COLORS[k];
      lg.appendChild(el("span", "", "", [i, document.createTextNode(PHASE[k])]));
    });
    wrap.appendChild(lg);
    return wrap;
  }

  function renderChart(run) {
    var est = run.estimate || [];
    if (!est.length) return el("div", "hint", "No turns to chart.");
    var w = 640, h = 190, L = 48, R = 12, T = 12, B = 26;
    var maxEst = 0; est.forEach(function (p) { if (p.tokens > maxEst) maxEst = p.tokens; });
    var win = run.context_window || 0;
    var winOff = win > 0 && win > maxEst * 4;
    var top = Math.max(maxEst, win && !winOff ? win : 0, run.compact_above || 0) * 1.08 || 1;
    var n = est.length;
    var x = function (i) { return n === 1 ? L + (w - L - R) / 2 : L + (w - L - R) * i / (n - 1); };
    var y = function (t) { return T + (h - T - B) * (1 - t / top); };
    var svg = sv("svg", { viewBox: "0 0 " + w + " " + h, role: "img", "aria-label": "Estimated prompt size per turn" });
    [0, 0.5, 1].forEach(function (f) {
      var t = top / 1.08 * f;
      svg.appendChild(sv("line", { x1: L, x2: w - R, y1: y(t), y2: y(t), stroke: "var(--bd)", "stroke-width": 1 }));
      svg.appendChild(sv("text", { x: L - 4, y: y(t) + 4, "text-anchor": "end" }, Math.round(t / 100) / 10 + "k"));
    });
    if (win && !winOff) {
      svg.appendChild(sv("line", { x1: L, x2: w - R, y1: y(win), y2: y(win), stroke: "var(--fail)", "stroke-width": 1.5, "stroke-dasharray": "6 3" }));
      svg.appendChild(sv("text", { x: w - R, y: y(win) - 3, "text-anchor": "end" }, "context window " + fmtN(win)));
    }
    if (run.compact_above && run.compact_above < top) {
      svg.appendChild(sv("line", { x1: L, x2: w - R, y1: y(run.compact_above), y2: y(run.compact_above), stroke: "var(--edit)", "stroke-width": 1, "stroke-dasharray": "2 3" }));
      svg.appendChild(sv("text", { x: L + 4, y: y(run.compact_above) - 3 }, "compact above " + fmtN(run.compact_above)));
    }
    est.forEach(function (p, i) {
      if (p.compacted) svg.appendChild(sv("line", { x1: x(i), x2: x(i), y1: T, y2: h - B, stroke: "var(--edit)", "stroke-width": 1.5 }));
    });
    svg.appendChild(sv("polyline", { points: est.map(function (p, i) { return x(i) + "," + y(p.tokens); }).join(" "), fill: "none", stroke: "var(--acc)", "stroke-width": 2 }));
    est.forEach(function (p, i) {
      var c = sv("circle", { cx: x(i), cy: y(p.tokens), r: p.compacted ? 4 : 2.5, fill: p.compacted ? "var(--edit)" : "var(--acc)" });
      c.appendChild(sv("title", {}, "turn " + p.turn + ": about " + fmtN(p.tokens) + " tokens" + (p.compacted ? " (older outputs shortened here)" : "")));
      svg.appendChild(c);
    });
    var step = Math.max(1, Math.ceil(n / 12));
    est.forEach(function (p, i) { if (i % step === 0 || i === n - 1) svg.appendChild(sv("text", { x: x(i), y: h - 8, "text-anchor": "middle" }, String(p.turn))); });
    var wrap = el("div");
    wrap.appendChild(svg);
    var cap = "Estimated from the transcript (characters / 3 + 800, as the agent counts), not measured; turn numbers along the bottom.";
    if (winOff) cap += " The context window (" + fmtN(win) + " tokens) is far above this run and is not drawn.";
    if (est.some(function (p) { return p.compacted; })) cap += " Orange marks are where older tool outputs would have been shortened.";
    wrap.appendChild(el("div", "hint", cap));
    return wrap;
  }

  function renderEvidence(run) {
    var box = el("div");
    function block(title, ev) {
      var c = el("div", "card");
      c.appendChild(el("div", "", "", [el("b", "", title + ": "), el("span", "badge " + (ev.verdict || "other"), ev.verdict || "?")]));
      var g = el("div", "kv");
      if (ev.required) g.appendChild(kv("required tests passing", ev.required_passed + " of " + ev.required));
      g.appendChild(kv("tests passed", String(ev.passed)));
      g.appendChild(kv("tests failed", String((ev.failed || []).length)));
      g.appendChild(kv("required but missing", String((ev.missing_required || []).length)));
      if (ev.verifier) g.appendChild(kv("verifier", ev.verifier));
      c.appendChild(g);
      (ev.reasons || []).forEach(function (r) { c.appendChild(el("div", "hint", r)); });
      if ((ev.failed || []).length) c.appendChild(details("failed tests (" + ev.failed.length + ")", pre(ev.failed.join("\n"))));
      if ((ev.missing_required || []).length) c.appendChild(details("missing required tests (" + ev.missing_required.length + ")", pre(ev.missing_required.join("\n"))));
      var rows = (ev.hashes || []).concat(ev.sandbox || []);
      if (rows.length) {
        var t = el("table");
        rows.forEach(function (r) { t.appendChild(el("tr", "", "", [el("td", "", r.key), el("td", "mono", r.val)])); });
        c.appendChild(details("hashes and sandbox", t));
      }
      return c;
    }
    if (run.evidence) box.appendChild(block("Final verification", run.evidence));
    else box.appendChild(el("div", "note", "No verification evidence file in this run directory."));
    if (run.baseline) box.appendChild(block("Baseline (before any change)", run.baseline));
    if (run.self_test) box.appendChild(el("div", "claim", "Agent's own regression test (live mode): " + run.self_test));
    return box;
  }

  function renderRun(run, col) {
    var root = el("div", "col");
    var title = el("h1", "", run.task);
    title.appendChild(document.createTextNode(" "));
    title.appendChild(el("span", "badge " + (/^(PASS|FAIL|ERROR|UNKNOWN)$/.test(run.verdict) ? run.verdict : "other"), run.verdict));
    root.appendChild(title);
    root.appendChild(el("div", "hint mono", run.dir));
    (run.notes || []).forEach(function (n) { root.appendChild(el("div", "note", n)); });

    if (run.over_claim) {
      root.appendChild(el("div", "claim over", "Over-claim: the agent said it fixed the issue, the verifier says " + run.verdict + (run.claim_against_own_tests ? ", and its own last test run had not passed (" + (run.last_own_test || "none") + ")." : ".")));
    } else {
      root.appendChild(el("div", "claim", run.claimed ? "The agent claimed a fix and the verdict is " + run.verdict + "." : "The agent did not claim a fix; the verdict is " + run.verdict + "."));
    }
    var g = el("div", "kv");
    var served = run.served_by ? Object.keys(run.served_by).map(function (k) { return k + " x" + run.served_by[k]; }).join(", ") : "";
    g.appendChild(kv("model", run.model + (served ? " (served by " + served + ")" : "")));
    g.appendChild(kv("harness", run.harness || "(not recorded)"));
    g.appendChild(kv("stop reason", run.stop_reason || "(not recorded)"));
    g.appendChild(kv("turns", run.turns + (run.max_turns ? " of " + run.max_turns : "")));
    g.appendChild(kv("sandbox runs", (run.test_runs + run.python_runs) + (run.max_sandbox_runs ? " of " + run.max_sandbox_runs : "") + " (" + run.test_runs + " tests, " + run.python_runs + " python)"));
    g.appendChild(kv("tokens", fmtN(run.prompt_tokens) + " prompt, " + fmtN(run.completion_tokens) + " output"));
    g.appendChild(kv("wall time", fmtMS(run.wall_ms)));
    if (run.host) g.appendChild(kv("host", run.host));
    root.appendChild(el("div", "card", "", [g]));
    if (run.infra_error) root.appendChild(el("div", "note", "Infrastructure problem: " + run.infra_error));
    (run.reasons || []).forEach(function (r) { root.appendChild(el("div", "hint", "verdict reason: " + r)); });
    if (run.issue) root.appendChild(details("issue the agent was given", longText(run.issue, 1500)));

    if (run.empty) {
      root.appendChild(el("div", "empty", "This run has no transcript: transcript.jsonl is missing or empty, so there is no timeline to replay."));
    } else {
      root.appendChild(el("h2", "", "Prompt size per turn (estimate)"));
      root.appendChild(renderChart(run));
      root.appendChild(el("h2", "", "Tools over time"));
      root.appendChild(renderStrip(run));
      root.appendChild(el("h2", "", "Timeline"));
      col.timeline = renderTimeline(run, col);
      root.appendChild(col.timeline);
    }
    root.appendChild(el("h2", "", "Final patch"));
    root.appendChild(run.patch ? patchBox(run.patch) : el("div", "hint", run.patch === "" ? "The final patch is empty: the agent changed nothing." : "No patch."));
    root.appendChild(el("h2", "", "Verification evidence"));
    root.appendChild(renderEvidence(run));
    return root;
  }

  var cols = [], active = 0;
  var cur = null;
  function setCur(col, t) {
    if (cur) cur.classList.remove("cur");
    cur = t; active = cols.indexOf(col);
    if (t) { t.classList.add("cur"); }
  }
  function visible(col) { return col.turns.filter(function (t) { return !t.classList.contains("hid"); }); }
  function step(dir) {
    var col = cols[active] || cols[0];
    if (!col) return;
    var v = visible(col);
    if (!v.length) return;
    var i = v.indexOf(cur);
    var j = i < 0 ? (dir > 0 ? 0 : v.length - 1) : Math.min(v.length - 1, Math.max(0, i + dir));
    setCur(col, v[j]);
    v[j].scrollIntoView({ block: "center", behavior: "smooth" });
  }

  // top bar
  var bar = el("div", "bar");
  var search = el("input"); search.type = "search"; search.placeholder = "Filter turns (tool names, paths, text, output)"; search.setAttribute("aria-label", "Filter turns");
  var count = el("span", "hint", "");
  var themeBtn = el("button", "", "theme"); themeBtn.type = "button";
  bar.appendChild(search); bar.appendChild(count); bar.appendChild(el("span", "hint", "j / k: next / previous turn, /: search"));
  bar.appendChild(themeBtn);
  app.appendChild(bar);

  var wrap = el("div", "cols" + (runs.length > 1 ? " two" : ""));
  runs.forEach(function (run) {
    var col = { turns: [] };
    cols.push(col);
    wrap.appendChild(renderRun(run, col));
  });
  app.appendChild(wrap);
  if (!runs.length) app.appendChild(el("div", "empty", "No run data."));

  function applyFilter() {
    var q = search.value.trim().toLowerCase(), shown = 0, total = 0;
    cols.forEach(function (c) { c.turns.forEach(function (t) {
      total++;
      var ok = !q || t.dataset.search.indexOf(q) >= 0;
      t.classList.toggle("hid", !ok);
      if (ok) shown++;
    }); });
    count.textContent = q ? shown + " of " + total + " steps" : "";
  }
  search.addEventListener("input", applyFilter);
  document.addEventListener("keydown", function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var tag = (e.target && e.target.tagName) || "";
    if (tag === "INPUT") { if (e.key === "Escape") { search.blur(); } return; }
    if (e.key === "j") { step(1); e.preventDefault(); }
    else if (e.key === "k") { step(-1); e.preventDefault(); }
    else if (e.key === "/") { search.focus(); e.preventDefault(); }
  });
  themeBtn.addEventListener("click", function () {
    var root = document.documentElement;
    var dark = root.getAttribute("data-theme") ? root.getAttribute("data-theme") === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
    root.setAttribute("data-theme", dark ? "light" : "dark");
  });
})();
