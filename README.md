# iavl-explore

Read-only viewer for a Cosmos SDK `application.db`. The same program serves the page. A click reads the node under the cursor. The database stays on the machine that opens it. This repository does not contain one.

The open does not take the LevelDB lock, and it refuses writes, so a node can keep the file.

## Run

```bash
go run . -db /path/to/application.db
```

Open http://127.0.0.1:8780/

| Flag | Meaning |
|---|---|
| `-db` | Path to the `application.db` directory. Required. |
| `-addr` | Listen address. Default `127.0.0.1:8780`. Terp KMS already uses `127.0.0.1:8791`. |
| `-web` | Directory that contains `index.html`. Empty serves the page built into the binary. |

Pass the `application.db` directory, not a copy of its tables and not a tarball. Any machine works. The path is only the flag.

## What a click reads

The page asks for six levels under the focus node. That is at most 127 records. It does not walk the rest of the store, and it does not scan every prefix in the file.

The store list is the Terp v6.4 set:

- IBC names and the names that stay mounted, SHA-256
- `b3-*` dest names, BLAKE3, mounted
- the twenty live names (`bank`, `wasm`, and the rest), SHA-256, not mounted after v6.4

The Nodes tab follows whichever of those names have a node key in the file you opened. The Migration tab walks the morocco-1 halt at block 23893261. On another chain those four checks do not match.

## Build

Go 1.26. The IAVL module is `github.com/cosmos/iavl`, replaced with `github.com/permissionlessweb/iavl` at `ecbbb63708f7` (BLAKE3 on `b3-*` trees, SHA-256 on the other names).

```bash
go test .
```
