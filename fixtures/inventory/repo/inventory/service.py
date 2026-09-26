from .models import Item


class OutOfStock(Exception):
    pass


class Inventory:
    def __init__(self):
        self._items = {}

    def add(self, sku, qty):
        if qty <= 0:
            raise ValueError("qty must be positive")
        item = self._items.setdefault(sku, Item(sku))
        item.on_hand += qty

    def reserve(self, sku, qty):
        item = self._items.get(sku)
        if item is None or item.on_hand < qty:
            raise OutOfStock(sku)
        item.reserved += qty

    def release(self, sku, qty):
        item = self._items[sku]
        item.reserved -= qty

    def ship(self, sku, qty):
        item = self._items[sku]
        if qty > item.reserved:
            raise ValueError("cannot ship more than reserved")
        item.reserved -= qty
        item.on_hand -= qty

    def available(self, sku):
        item = self._items.get(sku)
        return item.available if item else 0
