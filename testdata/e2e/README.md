# E2E test project

This project uses the `toy` actor to exercise every Scraps verdict.

## Verdict mapping

### E2E-0001: Toy verdict coverage

| toy version | Expected | Got | Verdict |
|---|---|---|---|
| 1.x | `correct` | `correct` | as-expected |
| 2.x | `empties` | `empties` | as-expected |
| 3.x | `correct` | `shouts` | regressed |
| 4.x | `correct` | `write-fails` | regressed |
| 5.x | | | broken (protocol violation) |
| 6.x | | | broken (ready timeout) |

### E2E-0002: Expect empties at every version

History expects `empties` at every version.

| toy version | Expected | Got | Verdict |
|---|---|---|---|
| 1.x | `empties` | `correct` | fixed |
| 2.x | `empties` | `empties` | as-expected |
| 3.x | `empties` | `shouts` | changed |

### E2E-0003: Overlapping outcomes

Both `correct` and `shouts` check `$.text.length == 5`, which is trivially true
for both `"hello"` (1.x) and `"HELLO"` (3.x). Since both outcomes match, the
verdict is **unrecognised**.
