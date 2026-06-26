"""A deliberately buggy example program for sl-dbg integration tests.

Run with sl-dbg:
    sl-dbg start --lang python --program examples/python/buggy.py --stop-on-entry

Try setting a conditional breakpoint:
    sl-dbg break examples/python/buggy.py:18 --if "item < 0"
"""

def compute(item: int) -> int:
    # Bug: silently misbehaves on negative inputs.
    return 1000 // item if item else 0


def process(items):
    total = 0
    for item in items:                # line 17
        x = compute(item)             # line 18
        total += x                    # line 19
    return total


if __name__ == "__main__":
    data = [10, 5, 2, 0, -1, 4]       # line 25
    result = process(data)            # line 26
    print(f"result={result}")         # line 27
