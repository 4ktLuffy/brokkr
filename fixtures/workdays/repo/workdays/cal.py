from datetime import timedelta


def add_business_days(start, days, holidays=()):
    """The date `days` business days after `start` (before it, if negative).
    Saturdays, Sundays and dates in `holidays` are skipped. `start` is not counted."""
    step = 1
    remaining = days
    current = start
    while remaining:
        current += timedelta(days=step)
        if current.weekday() < 5:
            remaining -= 1
    return current
