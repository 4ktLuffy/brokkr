#!/usr/bin/env python3
"""Mutants of a fix, for `brokkr mutate`. Standard library only, and it only
parses: nothing from the repository is imported or run.

stdin:  {"files": [{"path": "pkg/a.py", "orig": "...", "patched": "...", "new": false}]}
stdout: {"mutants": [{"kind", "path", "line", "desc", "diff"}], "skipped": [...]}

`orig` is the file before the fix and `patched` after it. A mutant is the
patched file with one small change, written as a unified diff against `orig`
(so it replaces the fix's own diff of that file). Two families:

  revert   one hunk of the fix put back as it was
  operator a change to an added line: comparison flipped, and/or swapped,
           condition negated, integer +-1, return value -> None, string -> '',
           True/False flipped, a simple statement deleted

A mutant is dropped when it does not parse, when its syntax tree equals that of
the fixed file (an equivalent mutant: nothing to learn), or when its tree was
already produced. "diff" turns the original into the mutant (what the sandbox
applies); "delta" turns the fixed file into the mutant (what a reviewer reads).
"""
import ast
import copy
import difflib
import json
import sys

CMP_FLIP = {
    ast.Eq: ast.NotEq, ast.NotEq: ast.Eq, ast.Lt: ast.GtE, ast.GtE: ast.Lt,
    ast.Gt: ast.LtE, ast.LtE: ast.Gt, ast.Is: ast.IsNot, ast.IsNot: ast.Is,
    ast.In: ast.NotIn, ast.NotIn: ast.In,
}
SIMPLE_STMT = (ast.Expr, ast.Assign, ast.AugAssign, ast.AnnAssign, ast.Return,
               ast.Raise, ast.Assert, ast.Delete)


def lines_of(text):
    return text.splitlines(keepends=True)


def udiff(path, orig, new, is_new):
    a, b = lines_of(orig), lines_of(new)
    out = ["diff --git a/%s b/%s\n" % (path, path)]
    if is_new:
        out.append("new file mode 100644\n")
    out.append("--- %s\n" % ("/dev/null" if is_new else "a/" + path))
    out.append("+++ b/%s\n" % path)
    body = []
    for l in difflib.unified_diff(a, b, n=3):
        if l.startswith(("--- ", "+++ ")) and len(body) == 0:
            continue
        body.append(l)
        if not l.endswith("\n"):
            body.append("\n\\ No newline at end of file\n")
    if not body:
        return ""  # the mutant is the original file: the patch leaves it out
    return "".join(out + body)


def added_lines(orig, patched):
    """1-based line numbers of `patched` that the fix added or changed."""
    a, b = lines_of(orig), lines_of(patched)
    sm = difflib.SequenceMatcher(None, a, b, autojunk=False)
    s = set()
    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag in ("replace", "insert"):
            s.update(range(j1 + 1, j2 + 1))
    return s


def hunk_reverts(orig, patched):
    a, b = lines_of(orig), lines_of(patched)
    sm = difflib.SequenceMatcher(None, a, b, autojunk=False)
    for group in sm.get_grouped_opcodes(3):
        ops = [o for o in group if o[0] != "equal"]
        if not ops:
            continue
        # Put back every change of this hunk: rebuild b with a's lines there.
        first, last = ops[0], ops[-1]
        new = b[:first[3]] + a[first[1]:last[2]] + b[last[4]:]
        # The hunk spans from its first to its last change; equal runs between
        # changes are identical in a and b, so the slice above is exact.
        yield "revert", first[3] + 1, "revert the hunk at line %d (lines %d-%d of the fixed file)" % (
            first[3] + 1, first[3] + 1, max(last[4], first[3] + 1)), "".join(new)


class Src:
    """Replace source segments by (line, byte column) positions."""

    def __init__(self, text):
        self.lines = [l.encode("utf-8") for l in lines_of(text)]

    def offset(self, line, col):
        return sum(len(l) for l in self.lines[:line - 1]) + col

    def replace(self, node, new):
        data = b"".join(self.lines)
        s = self.offset(node.lineno, node.col_offset)
        e = self.offset(node.end_lineno, node.end_col_offset)
        return (data[:s] + new.encode("utf-8") + data[e:]).decode("utf-8")


def snippet(src, node):
    return ast.get_source_segment("".join(l.decode("utf-8") for l in src.lines), node) or ""


def docstring_nodes(tree):
    out = set()
    for n in ast.walk(tree):
        if isinstance(n, (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            b = n.body
            if b and isinstance(b[0], ast.Expr) and isinstance(b[0].value, ast.Constant) and isinstance(b[0].value.value, str):
                out.add(id(b[0].value))
    return out


def fstring_parts(tree):
    out = set()
    for n in ast.walk(tree):
        if isinstance(n, ast.JoinedStr):
            for c in ast.walk(n):
                out.add(id(c))
    return out


def operator_mutants(patched, added):
    tree = ast.parse(patched)
    src = Src(patched)
    docs, fparts = docstring_nodes(tree), fstring_parts(tree)

    def on_added(n):
        return hasattr(n, "lineno") and n.lineno in added

    def un(n):
        return ast.unparse(n)

    for n in ast.walk(tree):
        if not on_added(n):
            continue
        ln = n.lineno
        if isinstance(n, ast.Compare):
            for i, op in enumerate(n.ops):
                if type(op) in CMP_FLIP:
                    m = copy.deepcopy(n)
                    m.ops[i] = CMP_FLIP[type(op)]()
                    yield "compare", ln, "flip comparison %s -> %s in `%s`" % (
                        type(op).__name__, type(m.ops[i]).__name__, snippet(src, n)), src.replace(n, un(m))
        elif isinstance(n, ast.BoolOp):
            m = copy.deepcopy(n)
            m.op = ast.Or() if isinstance(n.op, ast.And) else ast.And()
            yield "boolop", ln, "swap %s -> %s in `%s`" % (
                type(n.op).__name__.lower(), type(m.op).__name__.lower(), snippet(src, n)), src.replace(n, un(m))
        elif isinstance(n, ast.UnaryOp) and isinstance(n.op, ast.Not):
            yield "negate", ln, "drop `not` in `%s`" % snippet(src, n), src.replace(n, un(n.operand))
        elif isinstance(n, (ast.If, ast.While, ast.IfExp, ast.Assert)):
            t = n.test
            if on_added(t) and not (isinstance(t, ast.UnaryOp) and isinstance(t.op, ast.Not)):
                yield "negate", t.lineno, "negate condition `%s`" % snippet(src, t), src.replace(t, "not (%s)" % un(t))
        elif isinstance(n, ast.Constant) and id(n) not in fparts:
            v = n.value
            if isinstance(v, bool):
                yield "constant", ln, "flip %r -> %r" % (v, not v), src.replace(n, repr(not v))
            elif isinstance(v, int):
                for d in (1, -1):
                    yield "constant", ln, "integer %d -> %d" % (v, v + d), src.replace(n, repr(v + d))
            elif isinstance(v, str) and v != "" and id(n) not in docs:
                yield "string", ln, "string %s -> ''" % snippet(src, n)[:60], src.replace(n, "''")
        if isinstance(n, ast.Return) and n.value is not None and not (
                isinstance(n.value, ast.Constant) and n.value.value is None):
            yield "return", ln, "return None instead of `%s`" % snippet(src, n.value)[:60], src.replace(n, "return None")
        if isinstance(n, SIMPLE_STMT) and not (
                isinstance(n, ast.Expr) and isinstance(n.value, ast.Constant)):
            # Statements fully inside the added lines only: deleting a line
            # that was already there says nothing about the fix.
            end = getattr(n, "end_lineno", ln)
            if all(k in added for k in range(ln, end + 1)):
                yield "delete", ln, "delete `%s`" % snippet(src, n).splitlines()[0][:60], src.replace(n, "pass")


def main():
    req = json.load(sys.stdin)
    mutants, skipped = [], []
    for f in req["files"]:
        path, orig, patched, is_new = f["path"], f["orig"], f["patched"], bool(f.get("new"))
        try:
            ast.parse(patched)
        except SyntaxError as e:
            skipped.append("%s: fixed file does not parse (%s)" % (path, e.msg))
            continue
        # Equivalence is judged on the syntax tree, so a mutant that only
        # touches comments or layout (a reverted comment hunk) is dropped.
        seen = {ast.dump(ast.parse(patched))}
        added = added_lines(orig, patched)
        gens = list(hunk_reverts(orig, patched))
        try:
            gens += list(operator_mutants(patched, added))
        except Exception as e:  # a construct ast.unparse cannot handle
            skipped.append("%s: operator mutation failed (%s: %s)" % (path, type(e).__name__, e))
        for kind, line, desc, text in gens:
            try:
                key = ast.dump(ast.parse(text))
            except SyntaxError:
                continue
            if key in seen:
                continue
            seen.add(key)
            mutants.append({"kind": kind, "path": path, "line": line, "desc": desc,
                            "diff": udiff(path, orig, text, is_new),
                            "delta": udiff(path, patched, text, False)})
    json.dump({"mutants": mutants, "skipped": skipped}, sys.stdout)


if __name__ == "__main__":
    main()
