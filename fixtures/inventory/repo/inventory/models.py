from dataclasses import dataclass


@dataclass
class Item:
    sku: str
    on_hand: int = 0
    reserved: int = 0

    @property
    def available(self):
        """Units that can still be reserved."""
        return self.on_hand - self.reserved
