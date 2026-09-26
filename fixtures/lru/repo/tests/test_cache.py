import unittest

from lru.cache import LRUCache


class TestLRU(unittest.TestCase):
    def test_evicts_oldest(self):
        c = LRUCache(2)
        c.put("a", 1)
        c.put("b", 2)
        c.put("c", 3)
        self.assertIsNone(c.get("a"))
        self.assertEqual(len(c), 2)

    def test_get_refreshes(self):
        c = LRUCache(2)
        c.put("a", 1)
        c.put("b", 2)
        c.get("a")
        c.put("c", 3)
        self.assertEqual(c.get("a"), 1)
        self.assertIsNone(c.get("b"))

    def test_put_refreshes(self):
        c = LRUCache(2)
        c.put("a", 1)
        c.put("b", 2)
        c.put("a", 10)
        c.put("c", 3)
        self.assertEqual(c.get("a"), 10)
        self.assertIsNone(c.get("b"))

    def test_default(self):
        self.assertEqual(LRUCache(1).get("x", "d"), "d")

    def test_bad_capacity(self):
        with self.assertRaises(ValueError):
            LRUCache(0)
