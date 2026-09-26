import unittest
from datetime import date

from workdays.cal import add_business_days


class TestBusinessDays(unittest.TestCase):
    def test_zero(self):
        self.assertEqual(add_business_days(date(2026, 9, 23), 0), date(2026, 9, 23))

    def test_over_weekend(self):
        self.assertEqual(add_business_days(date(2026, 9, 25), 1), date(2026, 9, 28))

    def test_skips_holiday(self):
        self.assertEqual(add_business_days(date(2026, 9, 25), 1, holidays={date(2026, 9, 28)}), date(2026, 9, 29))

    def test_negative(self):
        self.assertEqual(add_business_days(date(2026, 9, 24), -2), date(2026, 9, 22))

    def test_negative_over_weekend(self):
        self.assertEqual(add_business_days(date(2026, 9, 28), -1), date(2026, 9, 25))
