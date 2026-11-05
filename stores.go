package main

// Store names match the Terp v6.4 mount set.
// IBC-facing names stay SHA-256. Dest names are BLAKE3.
// A name that is not in this list is not opened. The reader does not scan the file for prefixes.

var sha256Stores = map[string]struct{}{
	"ibc":                     {},
	"transfer":                {},
	"icahost":                 {},
	"icacontroller":           {},
	"packetfowardmiddleware":  {},
	"packetforwardmiddleware": {},
	"08-wasm":                 {},
	"capability":              {},
}

func migratableStores() []string {
	return []string{
		"bank",
		"staking",
		"acc",
		"crisis",
		"mint",
		"distribution",
		"slashing",
		"gov",
		"consensus",
		"feegrant",
		"evidence",
		"authz",
		"wasm",
		"feeshare",
		"globalfee",
		"drip",
		"smartaccount",
		"tokenfactory",
		"hashmerchant",
		"cw-hooks",
	}
}

func destStores() []string {
	src := migratableStores()
	out := make([]string, len(src))
	for i, name := range src {
		out[i] = "b3-" + name
	}
	return out
}

func algorithmName(storeName string) string {
	if _, ok := sha256Stores[storeName]; ok {
		return "sha256"
	}
	if len(storeName) > 3 && storeName[:3] == "b3-" {
		return "blake3"
	}
	return "sha256"
}
