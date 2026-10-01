"""High-confidence static checks on one Python file; never style.

Reports only things that fail when the code runs or compiles:
  - a name that is not defined anywhere it could come from
  - a module-level name used before its first definition or import
  - a duplicate argument name
  - return / yield outside a function, break / continue outside a loop

It only parses the file (ast.parse); nothing is imported or executed.

Usage: python3 -I check.py NEW [ORIG]
Prints one problem per line. With ORIG, only problems that are not already in
ORIG are printed, so an edit is blamed only for what it introduced. Exit 0 always.

What it deliberately does not do, and why: it does not flag unused imports or
names (style), local variables read before assignment inside a function (needs
flow analysis), attribute errors (needs types), or anything in a file with
`import *`, globals(), or exec (names can appear from outside the text).
"""
import ast
import builtins
import sys
import warnings

warnings.simplefilter("ignore")  # old repos have invalid escapes; we only parse

BUILTINS = set(dir(builtins)) | {
    "__file__", "__name__", "__doc__", "__path__", "__builtins__", "__spec__", "__loader__",
    "__package__", "__debug__", "__class__", "__module__", "__qualname__", "__annotations__",
    "WindowsError", "exit", "quit", "copyright", "credits", "license",
    # injected into the module by decorators such as sympy's @public
    "__all__",
    # Python 2 names: in repositories that still carry them they are nearly always
    # behind a version check, so a report would be noise
    "unicode", "xrange", "raw_input", "long", "basestring", "unichr", "file", "cmp",
    "execfile", "reload", "buffer", "intern", "apply", "coerce",
}
LOOPS = (ast.For, ast.AsyncFor, ast.While)
FUNCS = (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)
COMPS = (ast.ListComp, ast.SetComp, ast.GeneratorExp, ast.DictComp)


class Scope:
    def __init__(self, kind, parent):
        self.kind = kind          # module, class, function, comp
        self.parent = parent
        self.bind = {}            # name -> list of positions; None means "at an unknown time"
        self.globals = set()
        self.nonlocals = set()


def end_pos(node):
    return (getattr(node, "end_lineno", None) or node.lineno, getattr(node, "end_col_offset", None) or 0)


def start_pos(node):
    return (node.lineno, node.col_offset)


class Checker:
    def __init__(self, tree):
        self.tree = tree
        self.problems = []   # (line, kind, name, text)
        self.loads = []      # (name, pos, scope, in_loop, line)
        self.module = Scope("module", None)
        self.star = False
        self.dynamic = False
        self.future_annotations = False
        self.suppress = set()   # type-parameter names: not worth modelling
        self.stmt_end = None
        self.run()

    # ---- binding and loading

    def bind(self, scope, name, pos):
        if scope.kind == "function" and name in scope.globals:
            self.module.bind.setdefault(name, []).append(None)
            return
        scope.bind.setdefault(name, []).append(pos)

    def load(self, scope, name, node, in_loop, guarded):
        if guarded or name in BUILTINS:
            return
        self.loads.append((name, start_pos(node), scope, in_loop, node.lineno))

    def problem(self, line, kind, name, text):
        self.problems.append((line, kind, name, text))

    # ---- traversal

    def run(self):
        self.visit_body(self.tree.body, self.module, False, False, 0)
        if self.star or self.dynamic:
            self.problems = [p for p in self.problems if p[1] not in ("undefined", "before")]
            return
        self.resolve()

    def visit_body(self, body, scope, in_loop, guarded, loop_depth):
        for st in body:
            self.visit(st, scope, in_loop, guarded, loop_depth)

    def targets(self, t, scope, pos):
        """Bind the names an assignment target stores to; load what it only reads."""
        if isinstance(t, ast.Name):
            self.bind(scope, t.id, pos or start_pos(t))
        elif isinstance(t, (ast.Tuple, ast.List)):
            for e in t.elts:
                self.targets(e, scope, pos)
        elif isinstance(t, ast.Starred):
            self.targets(t.value, scope, pos)
        else:  # attribute / subscript targets read their base
            self.visit(t, scope, False, False, 0)

    def catches_nameerror(self, node):
        for h in node.handlers:
            if h.type is None:
                return True
            types = h.type.elts if isinstance(h.type, ast.Tuple) else [h.type]
            for t in types:
                n = t.id if isinstance(t, ast.Name) else getattr(t, "attr", "")
                if n in ("NameError", "Exception", "BaseException"):
                    return True
        return False

    def annotation(self, node, scope, in_loop, guarded):
        if node is not None and not self.future_annotations:
            self.visit(node, scope, in_loop, guarded, 0)

    def function(self, node, scope, in_loop, guarded):
        a = node.args
        for d in a.defaults + [d for d in a.kw_defaults if d is not None]:
            self.visit(d, scope, in_loop, guarded, 0)
        allargs = a.posonlyargs + a.args + a.kwonlyargs + ([a.vararg] if a.vararg else []) + ([a.kwarg] if a.kwarg else [])
        for x in allargs:
            self.annotation(x.annotation, scope, in_loop, guarded)
        if not isinstance(node, ast.Lambda):
            self.annotation(node.returns, scope, in_loop, guarded)
        for tp in getattr(node, "type_params", []) or []:
            self.suppress.add(tp.name)
        fs = Scope("function", scope)
        seen = set()
        for x in allargs:
            if x.arg in seen:
                self.problem(x.lineno, "duplicate-arg", x.arg, "duplicate argument %r in %s" % (x.arg, getattr(node, "name", "lambda")))
            seen.add(x.arg)
            fs.bind.setdefault(x.arg, []).append(None)
        body = [node.body] if isinstance(node, ast.Lambda) else node.body
        for st in body:
            self.visit(st, fs, False, False, 0)

    def visit(self, node, scope, in_loop, guarded, loop_depth):
        v = self.visit
        t = type(node)
        if t is ast.Name:
            if isinstance(node.ctx, ast.Load):
                self.load(scope, node.id, node, in_loop, guarded)
            elif isinstance(node.ctx, ast.Store):
                self.bind(scope, node.id, start_pos(node))
            return
        if t in (ast.FunctionDef, ast.AsyncFunctionDef):
            for d in node.decorator_list:
                v(d, scope, in_loop, guarded, 0)
            self.bind(scope, node.name, end_pos(node))
            self.function(node, scope, in_loop, guarded)
            return
        if t is ast.Lambda:
            self.function(node, scope, in_loop, guarded)
            return
        if t is ast.ClassDef:
            for d in node.decorator_list + node.bases + [k.value for k in node.keywords]:
                v(d, scope, in_loop, guarded, 0)
            self.bind(scope, node.name, end_pos(node))
            for tp in getattr(node, "type_params", []) or []:
                self.suppress.add(tp.name)
            cs = Scope("class", scope)
            # a class body runs where it is defined: keep the loop flag
            self.visit_body(node.body, cs, in_loop, guarded, 0)
            return
        if t in COMPS:
            cs = Scope("comp", scope)
            for i, g in enumerate(node.generators):
                v(g.iter, scope if i == 0 else cs, in_loop, guarded, 0)
                self.targets(g.target, cs, None)
                for c in g.ifs:
                    v(c, cs, in_loop, guarded, 0)
            if t is ast.DictComp:
                v(node.key, cs, in_loop, guarded, 0)
                v(node.value, cs, in_loop, guarded, 0)
            else:
                v(node.elt, cs, in_loop, guarded, 0)
            return
        if t is ast.NamedExpr:
            v(node.value, scope, in_loop, guarded, 0)
            s = scope
            while s.kind == "comp":
                s = s.parent
            self.bind(s, node.target.id, end_pos(node))
            return
        if t in (ast.Assign, ast.AnnAssign, ast.AugAssign):
            pos = end_pos(node)
            if t is ast.Assign:
                v(node.value, scope, in_loop, guarded, 0)
                for tg in node.targets:
                    self.targets(tg, scope, pos)
            elif t is ast.AnnAssign:
                self.annotation(node.annotation, scope, in_loop, guarded)
                if node.value is not None:
                    v(node.value, scope, in_loop, guarded, 0)
                    self.targets(node.target, scope, pos)
                elif not isinstance(node.target, ast.Name):
                    self.targets(node.target, scope, pos)
            else:
                if isinstance(node.target, ast.Name):
                    self.load(scope, node.target.id, node.target, in_loop, guarded)
                v(node.value, scope, in_loop, guarded, 0)
                self.targets(node.target, scope, pos)
            return
        if t is ast.Import:
            for a in node.names:
                self.bind(scope, a.asname or a.name.split(".")[0], end_pos(node))
            return
        if t is ast.ImportFrom:
            for a in node.names:
                if a.name == "*":
                    self.star = True
                else:
                    self.bind(scope, a.asname or a.name, end_pos(node))
            if node.module == "__future__" and any(a.name == "annotations" for a in node.names):
                self.future_annotations = True
            return
        if t is ast.Global:
            scope.globals.update(node.names)
            for n in node.names:
                self.module.bind.setdefault(n, []).append(None)
            return
        if t is ast.Nonlocal:
            scope.nonlocals.update(node.names)
            return
        if t in (ast.For, ast.AsyncFor):
            v(node.iter, scope, in_loop, guarded, loop_depth)
            self.targets(node.target, scope, None)
            self.visit_body(node.body, scope, True, guarded, loop_depth + 1)
            self.visit_body(node.orelse, scope, in_loop, guarded, loop_depth)
            return
        if t is ast.While:
            v(node.test, scope, True, guarded, loop_depth + 1)
            self.visit_body(node.body, scope, True, guarded, loop_depth + 1)
            self.visit_body(node.orelse, scope, in_loop, guarded, loop_depth)
            return
        if t in (ast.Try,) or t.__name__ == "TryStar":
            g = guarded or self.catches_nameerror(node)
            self.visit_body(node.body, scope, in_loop, g, loop_depth)
            for h in node.handlers:
                if h.type is not None:
                    v(h.type, scope, in_loop, guarded, loop_depth)
                if h.name:
                    self.bind(scope, h.name, start_pos(h))
                self.visit_body(h.body, scope, in_loop, guarded, loop_depth)
            self.visit_body(node.orelse, scope, in_loop, g, loop_depth)
            self.visit_body(node.finalbody, scope, in_loop, guarded, loop_depth)
            return
        if t in (ast.With, ast.AsyncWith):
            for it in node.items:
                v(it.context_expr, scope, in_loop, guarded, loop_depth)
                if it.optional_vars is not None:
                    self.targets(it.optional_vars, scope, None)
            self.visit_body(node.body, scope, in_loop, guarded, loop_depth)
            return
        if t.__name__ in ("MatchAs", "MatchStar"):
            if node.name:
                self.bind(scope, node.name, start_pos(node))
        elif t.__name__ == "MatchMapping" and node.rest:
            self.bind(scope, node.rest, start_pos(node))
        elif t.__name__ == "TypeAlias":
            self.bind(scope, node.name.id, None)
            for tp in node.type_params:
                self.suppress.add(tp.name)
            return
        elif t is ast.Return:
            if scope.kind != "function":
                self.problem(node.lineno, "syntax", "return", "'return' outside function")
        elif t in (ast.Yield, ast.YieldFrom):
            if scope.kind in ("module", "class"):
                self.problem(node.lineno, "syntax", "yield", "'yield' outside function")
        elif t in (ast.Break, ast.Continue):
            if loop_depth == 0:
                w = "break" if t is ast.Break else "continue"
                self.problem(node.lineno, "syntax", w, "'%s' outside loop" % w)
        elif t is ast.Call:
            f = node.func
            if isinstance(f, ast.Name) and f.id in ("globals", "exec"):
                self.dynamic = True
        for c in ast.iter_child_nodes(node):
            # a loop flag does not cross into a function or class; handled above
            v(c, scope, in_loop, guarded, loop_depth)

    # ---- resolution

    def resolve(self):
        for name, pos, scope, in_loop, line in self.loads:
            if name in self.suppress:
                continue
            # chain of scopes the name is looked up in: class scopes only when the
            # load is directly in them
            chain, s, immediate = [], scope, True
            while s is not None:
                if s.kind == "class" and s is not scope:
                    s = s.parent
                    continue
                if s.kind in ("function", "comp"):
                    immediate = False
                if s.kind == "function" and name in s.globals:
                    chain.append(self.module)
                    break
                if s.kind == "function" and name in s.nonlocals:
                    chain = None
                    break
                chain.append(s)
                s = s.parent
            if chain is None:
                continue
            any_binding, any_before = False, False
            for sc in chain:
                ps = sc.bind.get(name)
                if not ps:
                    continue
                any_binding = True
                if any(p is None or p < pos for p in ps):
                    any_before = True
            if not any_binding:
                self.problem(line, "undefined", name, "undefined name %r" % name)
            elif immediate and not any_before and not in_loop:
                first = min(p for sc in chain for p in sc.bind.get(name, []) if p)[0]
                self.problem(line, "before", name, "%r is used before it is defined (first defined at line %d)" % (name, first))


def check(src, path="<file>"):
    try:
        tree = ast.parse(src, path)
    except (SyntaxError, ValueError, RecursionError, MemoryError):
        return []   # the syntax check reports this
    return sorted(Checker(tree).problems)


def key(p):
    return (p[1], p[2])


def new_problems(new, old):
    left = {}
    for p in old:
        left[key(p)] = left.get(key(p), 0) + 1
    out = []
    for p in new:
        if left.get(key(p), 0) > 0:
            left[key(p)] -= 1
        else:
            out.append(p)
    return out


def read(path):
    try:
        with open(path, "rb") as f:
            return f.read()
    except OSError:
        return None


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: check.py NEW [ORIG]")
    if sys.argv[1] == "--raw":  # measurement: every problem in each file
        for p in sys.argv[2:]:
            src = read(p)
            if src is None:
                continue
            for line, kind, name, text in check(src, p):
                print("%s:%d: %s" % (p, line, text))
        return
    new = check(read(sys.argv[1]) or b"", sys.argv[1])
    if len(sys.argv) > 2:
        old = read(sys.argv[2])
        new = new_problems(new, check(old, sys.argv[2]) if old is not None else [])
    for line, kind, name, text in new[:10]:
        print("line %d: %s" % (line, text))
    if len(new) > 10:
        print("(%d more)" % (len(new) - 10))


if __name__ == "__main__":
    main()
