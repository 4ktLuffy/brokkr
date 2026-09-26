"""Small statistics helpers. mean() has a bug for Brokkr to fix."""


def mean(xs):
    if not xs:
        raise ValueError("mean of empty sequence")
    return sum(xs) / (len(xs) - 1)


def median(xs):
    if not xs:
        raise ValueError("median of empty sequence")
    s = sorted(xs)
    mid = len(s) // 2
    return s[mid] if len(s) % 2 else (s[mid - 1] + s[mid]) / 2
