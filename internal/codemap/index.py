"""Code index for the Brokkr agent: find_definition, find_usages, outline.

Reads a repository tree and answers questions about its Python code. It only
parses (ast.parse) and never imports or executes anything from the repository.
Stdlib only.

Usage:  python3 -I index.py ROOT         serve JSON-line requests on stdin
        python3 -I index.py ROOT OP ARG  answer one request and exit

A request is {"op": "find_definition"|"find_usages"|"outline"|"stats", "arg": ...}
and the reply is {"text": ...}. The index is built on the first request and
kept in memory. Before every request the tree is re-walked and only files whose
size or mtime changed are parsed again, so the agent's edits show up without a
rebuild.
"""
import ast
import difflib
import json
import os
import sys
import time
import warnings

warnings.simplefilter("ignore")  # old repos have invalid escapes; we only parse

MAX_DEFS = 15          # find_definition results shown
MAX_USAGE_LINES = 40   # find_usages lines shown
MAX_USAGE_FILES = 15
MAX_PER_FILE = 6
MAX_FILE_BYTES = 1 << 20
SKIP_DIRS = {"__pycache__", "node_modules", "site-packages", "build", "dist", "venv", "env"}

# usage kinds, packed into the low 3 bits of a reference
CALL, ATTR, NAME, IMPORT, ASSIGN, KWARG = range(6)
KIND = ["call", "attr", "name", "import", "assign", "kwarg"]


class FileInfo:
    __slots__ = ("stamp", "ok", "defs", "refs", "nlines", "mod", "error")

    def __init__(self, stamp):
        self.stamp = stamp
        self.ok = False
        self.defs = []   # (simple, qual, kind, line, end, sig, doc)
        self.refs = {}   # name -> list of line*8+kind
        self.nlines = 0
        self.mod = ""
        self.error = ""


def is_test_path(rel):
    parts = rel.split("/")
    base = parts[-1]
    return ("tests" in parts or "test" in parts or "testing" in parts
            or base.startswith("test_") or base.endswith("_tests.py") or base == "conftest.py")


def mod_name(rel):
    m = rel[:-3] if rel.endswith(".py") else rel
    m = m.replace("/", ".")
    if m.endswith(".__init__"):
        m = m[: -len(".__init__")]
    return m


def args_sig(a):
    try:
        return ast.unparse(a)
    except Exception:
        return "..."


def clip(s, n):
    s = " ".join(s.split())
    return s if len(s) <= n else s[: n - 3] + "..."


def doc_line(node):
    try:
        d = ast.get_docstring(node, clean=True)
    except Exception:
        return ""
    if not d:
        return ""
    for line in d.splitlines():
        if line.strip():
            return clip(line.strip(), 100)
    return ""


def func_sig(node):
    pre = "async def " if isinstance(node, ast.AsyncFunctionDef) else "def "
    sig = "%s%s(%s)" % (pre, node.name, args_sig(node.args))
    if node.returns is not None:
        sig += " -> " + args_sig(node.returns)
    return clip(sig, 160)


def class_sig(node):
    bases = [args_sig(b) for b in node.bases] + ["%s=%s" % (k.arg, args_sig(k.value)) for k in node.keywords if k.arg]
    return clip("class %s(%s)" % (node.name, ", ".join(bases)) if bases else "class %s" % node.name, 160)


def collect_defs(body, prefix, out, in_class):
    """Definitions in a module or class body, descending into if/try/with."""
    for node in body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            qual = prefix + node.name
            kind = "method" if in_class else "function"
            out.append((node.name, qual, kind, node.lineno, node.end_lineno or node.lineno, func_sig(node), doc_line(node)))
        elif isinstance(node, ast.ClassDef):
            qual = prefix + node.name
            out.append((node.name, qual, "class", node.lineno, node.end_lineno or node.lineno, class_sig(node), doc_line(node)))
            collect_defs(node.body, qual + ".", out, True)
        elif isinstance(node, (ast.Assign, ast.AnnAssign, ast.AugAssign)):
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            if isinstance(node, ast.AugAssign):
                continue
            val = getattr(node, "value", None)
            for t in targets:
                for n in names_of(t):
                    qual = prefix + n
                    sig = n
                    if isinstance(node, ast.AnnAssign):
                        sig += ": " + args_sig(node.annotation)
                    if val is not None:
                        sig += " = " + args_sig(val)
                    out.append((n, qual, "attribute" if in_class else "variable", node.lineno,
                                node.end_lineno or node.lineno, clip(sig, 120), ""))
        elif isinstance(node, (ast.If, ast.With, ast.AsyncWith)):
            collect_defs(node.body, prefix, out, in_class)
            if isinstance(node, ast.If):
                collect_defs(node.orelse, prefix, out, in_class)
        elif isinstance(node, ast.Try) or node.__class__.__name__ == "TryStar":
            collect_defs(node.body, prefix, out, in_class)
            for h in node.handlers:
                collect_defs(h.body, prefix, out, in_class)
            collect_defs(node.orelse, prefix, out, in_class)
            collect_defs(node.finalbody, prefix, out, in_class)


def names_of(t):
    if isinstance(t, ast.Name):
        return [t.id]
    if isinstance(t, (ast.Tuple, ast.List)):
        r = []
        for e in t.elts:
            r += names_of(e)
        return r
    return []


def collect_refs(tree):
    refs = {}

    def add(name, line, kind):
        refs.setdefault(name, []).append(line * 8 + kind)

    called = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            called.add(id(node.func))
    for node in ast.walk(tree):
        if isinstance(node, ast.Name):
            if isinstance(node.ctx, ast.Load):
                add(node.id, node.lineno, CALL if id(node) in called else NAME)
            elif isinstance(node.ctx, ast.Store):
                add(node.id, node.lineno, ASSIGN)
        elif isinstance(node, ast.Attribute):
            # the attribute name sits at the end of the expression
            line = node.end_lineno or node.lineno
            if isinstance(node.ctx, ast.Store):
                add(node.attr, line, ASSIGN)
            else:
                add(node.attr, line, CALL if id(node) in called else ATTR)
        elif isinstance(node, ast.ImportFrom):
            for a in node.names:
                if a.name != "*":
                    add(a.name, getattr(a, "lineno", node.lineno), IMPORT)
        elif isinstance(node, ast.Import):
            for a in node.names:
                add(a.name.split(".")[-1], getattr(a, "lineno", node.lineno), IMPORT)
        elif isinstance(node, ast.keyword) and node.arg:
            add(node.arg, getattr(node, "lineno", 0) or node.value.lineno, KWARG)
    return refs


def parse_file(path, rel, stamp):
    fi = FileInfo(stamp)
    fi.mod = mod_name(rel)
    try:
        with open(path, "rb") as f:
            raw = f.read()
        tree = ast.parse(raw, path)
    except (SyntaxError, ValueError, RecursionError, MemoryError) as e:
        fi.error = "%s: %s" % (type(e).__name__, getattr(e, "msg", str(e)))
        return fi
    except OSError as e:
        fi.error = str(e)
        return fi
    fi.nlines = raw.count(b"\n") + 1
    collect_defs(tree.body, "", fi.defs, False)
    fi.refs = collect_refs(tree)
    fi.ok = True
    return fi


class Index:
    def __init__(self, root):
        self.root = root
        self.files = {}
        self.by_name = {}   # simple name -> [(rel, def)]
        self.built = False
        self.build_ms = 0
        self.last_refresh = ""

    def walk(self):
        found = {}
        for dp, dns, fns in os.walk(self.root):
            dns[:] = [d for d in dns if d not in SKIP_DIRS and not d.startswith(".")]
            for fn in fns:
                if not fn.endswith(".py"):
                    continue
                p = os.path.join(dp, fn)
                try:
                    st = os.stat(p)
                except OSError:
                    continue
                if st.st_size > MAX_FILE_BYTES or not os.path.isfile(p):
                    continue
                rel = os.path.relpath(p, self.root).replace(os.sep, "/")
                found[rel] = (st.st_mtime_ns, st.st_size)
        return found

    def refresh(self):
        """Bring the index in line with the tree; returns how many files were parsed."""
        t0 = time.time()
        found = self.walk()
        changed = 0
        for rel in list(self.files):
            if rel not in found:
                del self.files[rel]
                changed += 1
        for rel, stamp in found.items():
            old = self.files.get(rel)
            if old is None or old.stamp != stamp:
                self.files[rel] = parse_file(os.path.join(self.root, rel), rel, stamp)
                changed += 1
        if changed or not self.built:
            self.by_name = {}
            for rel, fi in self.files.items():
                for d in fi.defs:
                    self.by_name.setdefault(d[0], []).append((rel, d))
        if not self.built:
            self.built = True
            self.build_ms = int((time.time() - t0) * 1000)
        self.last_refresh = "%d ms, %d changed" % (int((time.time() - t0) * 1000), changed)
        return changed

    def unparsed(self):
        return sum(1 for f in self.files.values() if not f.ok)

    # ---- find_definition

    def rank(self, rel, d):
        # exact simple-name hits matter most; within them, library before tests,
        # classes and functions before attributes, shallower paths first
        return (is_test_path(rel), d[2] in ("variable", "attribute"), rel.count("/"), rel, d[3])

    def find_definition(self, query):
        query = query.strip()
        if not query:
            return "error: name is empty. Give a class, function, method (Class.method) or module-level name."
        parts = [p for p in query.replace("::", ".").split(".") if p]
        if not parts:
            return "error: %r is not a name" % query
        last, qual = parts[-1], parts[:-1]
        cands = list(self.by_name.get(last, []))
        note = ""
        if qual and cands:
            full = ".".join(parts)
            # Class.method matches within a class; package.module.name matches by module path
            narrowed = [(rel, d) for rel, d in cands
                        if d[1] == full or d[1].endswith("." + full)
                        or (self.files[rel].mod + "." + d[1]).endswith("." + full)
                        or (self.files[rel].mod + "." + d[1]) == full]
            if narrowed:
                cands = narrowed
            else:
                note = "no exact match for %r; showing every definition named %r\n" % (query, last)
        exact = bool(cands)
        if not cands:
            # partial: case-insensitive substring of a simple name
            low = last.lower()
            for name, items in self.by_name.items():
                if low in name.lower():
                    cands += items
            if cands:
                note = "no definition named %r; these names contain it\n" % query
        if not cands:
            return self.not_found(query, last)
        cands.sort(key=lambda x: self.rank(*x) if exact else (len(x[1][0]), self.rank(*x)))
        total = len(cands)
        shown = cands[:MAX_DEFS]
        out = [note.rstrip()] if note else []
        for rel, d in shown:
            simple, qual_, kind, line, end, sig, doc = d
            # the signature already says def/class; only plain names need their kind
            label = sig if kind in ("function", "method", "class") else "%s %s" % (kind, sig)
            s = "%s:%d-%d  %s" % (rel, line, end, label)
            if kind in ("method", "attribute") and "." in qual_:
                s += "  [in %s]" % qual_.rsplit(".", 1)[0]
            if doc:
                s += "  -- " + doc
            out.append(s)
        if total > len(shown):
            out.append("[%d definitions in total; %d shown. Qualify the name (Class.method or package.module.name) to narrow it.]" % (total, len(shown)))
        return "\n".join(out)

    def not_found(self, query, last):
        names = list(self.by_name)
        close = difflib.get_close_matches(last, names, n=5, cutoff=0.7)
        msg = "no class, function, method or module-level name %r in %d indexed Python files" % (query, len(self.files))
        if close:
            msg += ". Similar names: " + ", ".join(close)
        msg += ". It may be defined dynamically, in a non-Python file, or in a file that does not parse"
        if self.unparsed():
            msg += " (%d files could not be parsed)" % self.unparsed()
        return msg + ". Try the search tool with a regular expression."

    # ---- find_usages

    def find_usages(self, query):
        query = query.strip()
        parts = [p for p in query.replace("::", ".").split(".") if p]
        if not parts:
            return "error: name is empty. Give the name of a function, class, method or variable."
        last = parts[-1]
        owner = parts[-2] if len(parts) > 1 else ""
        hits = []   # (rel, line, kind, mentions_owner)
        defs = self.by_name.get(last, [])
        # the assignment that defines a variable is the definition, not a usage
        defined_at = set((rel, d[3]) for rel, d in defs if d[2] in ("variable", "attribute"))
        for rel, fi in self.files.items():
            lst = fi.refs.get(last)
            if not lst:
                continue
            mention = bool(owner) and owner in fi.refs
            for r in lst:
                if r & 7 == ASSIGN and (rel, r >> 3) in defined_at:
                    continue
                hits.append((rel, r >> 3, r & 7, mention))
        if not hits:
            if not defs:
                return self.not_found(query, last).replace("no class, function, method or module-level name", "no definition or reference of", 1)
            return "%r is defined (%s) but nothing in the indexed files references it by name. It may be called dynamically, from a non-Python file, or only from outside the repository." % (
                query, "; ".join("%s:%d" % (rel, d[3]) for rel, d in defs[:3]))
        # group by file; files that also mention the owner class first, library before tests
        byfile = {}
        for h in hits:
            byfile.setdefault(h[0], []).append(h)
        order = sorted(byfile, key=lambda rel: (not byfile[rel][0][3], is_test_path(rel), -len(byfile[rel]), rel))
        out = []
        if owner:
            out.append("references to the name %r (the index does not know types, so these are all uses of that name; files that also mention %s come first)" % (last, owner))
        lines_shown = files_shown = 0
        cache = {}
        for rel in order:
            if files_shown >= MAX_USAGE_FILES or lines_shown >= MAX_USAGE_LINES:
                break
            files_shown += 1
            hs = sorted(set((h[1], h[2]) for h in byfile[rel]))
            out.append("%s (%d)" % (rel, len(hs)))
            src = self.source(rel, cache)
            for line, kind in hs[:MAX_PER_FILE]:
                if lines_shown >= MAX_USAGE_LINES:
                    break
                text = src[line - 1].strip() if 0 < line <= len(src) else ""
                out.append("  %d: [%s] %s" % (line, KIND[kind], clip(text, 140)))
                lines_shown += 1
            if len(hs) > MAX_PER_FILE:
                out.append("  ... %d more in this file" % (len(hs) - MAX_PER_FILE))
        total_files = len(order)
        total_hits = sum(len(set((h[1], h[2]) for h in v)) for v in byfile.values())
        if files_shown < total_files or lines_shown < total_hits:
            out.append("[%d usages in %d files in total; the most relevant are shown. Search a narrower name or use the search tool with a path.]" % (total_hits, total_files))
        others = [(rel, d) for rel, d in defs if d[2] in ("function", "method", "class")]
        if len(others) > 1:
            others.sort(key=lambda x: self.rank(*x))
            out.append("Also defined (%d places, same name; a fix may be needed in each): %s" % (
                len(others), ", ".join("%s:%d %s" % (rel, d[3], d[1]) for rel, d in others[:8])
                + (" ..." if len(others) > 8 else "")))
        return "\n".join(out)

    def source(self, rel, cache):
        if rel not in cache:
            try:
                with open(os.path.join(self.root, rel), "rb") as f:
                    cache[rel] = f.read().decode("utf-8", "replace").split("\n")
            except OSError:
                cache[rel] = []
        return cache[rel]

    # ---- outline

    def outline(self, path):
        rel = path.strip().replace("\\", "/")
        while rel.startswith("./"):
            rel = rel[2:]
        if not rel:
            return "error: path is empty. Give a Python file path relative to the repository root."
        if not rel.endswith(".py"):
            return "error: outline works on .py files only; use read_file for %s" % rel
        fi = self.files.get(rel)
        if fi is None:
            base = rel.rsplit("/", 1)[-1]
            same = sorted(r for r in self.files if r.rsplit("/", 1)[-1] == base)
            close = difflib.get_close_matches(rel, list(self.files), n=4, cutoff=0.6)
            hint = same[:5] or close
            return "error: %s is not an indexed Python file%s" % (rel, (". Did you mean: " + ", ".join(hint)) if hint else ". Use list_dir to find it")
        if not fi.ok:
            return "error: %s could not be parsed (%s); read it with read_file" % (rel, fi.error)
        out = ["%s: %d lines" % (rel, fi.nlines)]
        vars_ = [d for d in fi.defs if d[2] == "variable"]
        for simple, qual, kind, line, end, sig, doc in fi.defs:
            if kind in ("variable",):
                continue
            depth = qual.count(".")
            if kind == "attribute":
                continue
            s = "%s%d-%d  %s" % ("  " * depth, line, end, sig)
            if doc:
                s += "  -- " + doc
            out.append(s)
        if vars_:
            names = []
            for d in vars_:
                if d[0] not in names:
                    names.append(d[0])
            out.append("module variables (%d): %s" % (len(names), clip(", ".join(names), 400)))
        if len(out) > 400:
            out = out[:400] + ["[outline cut at 400 lines]"]
        return "\n".join(out)

    def stats(self):
        defs = sum(len(f.defs) for f in self.files.values())
        refs = sum(len(v) for f in self.files.values() for v in f.refs.values())
        return {"files": len(self.files), "unparsed": self.unparsed(), "defs": defs, "refs": refs,
                "build_ms": self.build_ms, "last_refresh": self.last_refresh}


def handle(ix, req):
    ix.refresh()
    op, arg = req.get("op"), req.get("arg", "")
    if op == "find_definition":
        return ix.find_definition(arg)
    if op == "find_usages":
        return ix.find_usages(arg)
    if op == "outline":
        return ix.outline(arg)
    if op == "stats":
        return json.dumps(ix.stats())
    return "error: unknown op %r" % op


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: index.py ROOT [OP ARG]")
    ix = Index(os.path.abspath(sys.argv[1]))
    if len(sys.argv) >= 4:
        print(handle(ix, {"op": sys.argv[2], "arg": sys.argv[3]}))
        return
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            text = handle(ix, json.loads(line))
        except Exception as e:  # a bad file must never take the agent down
            text = "error: code index failed: %s: %s" % (type(e).__name__, e)
        sys.stdout.write(json.dumps({"text": text}) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
