import unittest

from inventory.service import Inventory, OutOfStock


class TestInventory(unittest.TestCase):
    def setUp(self):
        self.inv = Inventory()
        self.inv.add("A", 10)

    def test_reserve_within_stock(self):
        self.inv.reserve("A", 8)
        self.assertEqual(self.inv.available("A"), 2)

    def test_no_double_booking(self):
        self.inv.reserve("A", 8)
        with self.assertRaises(OutOfStock):
            self.inv.reserve("A", 5)

    def test_unknown_sku(self):
        with self.assertRaises(OutOfStock):
            self.inv.reserve("B", 1)

    def test_release_too_much(self):
        self.inv.reserve("A", 3)
        with self.assertRaises(ValueError):
            self.inv.release("A", 4)

    def test_release_then_reserve(self):
        self.inv.reserve("A", 10)
        self.inv.release("A", 4)
        self.inv.reserve("A", 4)
        self.assertEqual(self.inv.available("A"), 0)

    def test_ship(self):
        self.inv.reserve("A", 3)
        self.inv.ship("A", 3)
        self.assertEqual(self.inv.available("A"), 7)

    def test_add_invalid(self):
        with self.assertRaises(ValueError):
            self.inv.add("A", 0)
