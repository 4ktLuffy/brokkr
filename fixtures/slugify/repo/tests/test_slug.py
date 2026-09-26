import unittest

from textkit.slug import slugify


class TestSlugify(unittest.TestCase):
    def test_basic(self):
        self.assertEqual(slugify("Hello, World!"), "hello-world")

    def test_accents_folded(self):
        self.assertEqual(slugify("Caf\u00e9 D\u00e9j\u00e0 Vu"), "cafe-deja-vu")

    def test_german(self):
        self.assertEqual(slugify("\u00dcber Stra\u00dfe"), "uber-strae")

    def test_max_len_no_trailing_sep(self):
        self.assertEqual(slugify("hello world again", max_len=6), "hello")

    def test_custom_sep(self):
        self.assertEqual(slugify("a b  c", sep="_"), "a_b_c")

    def test_only_symbols(self):
        self.assertEqual(slugify("!!!"), "")
