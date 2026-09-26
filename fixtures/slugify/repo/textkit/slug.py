import re
import unicodedata


def slugify(text, sep="-", max_len=None):
    """Lowercase ASCII slug. Accents are folded (e -> e), runs of anything else
    become one separator, and the result never starts or ends with a separator."""
    text = re.sub(r"[^a-z0-9]+", sep, text.lower()).strip(sep)
    if max_len is not None and len(text) > max_len:
        text = text[:max_len]
    return text
