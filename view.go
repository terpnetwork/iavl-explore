package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sync"
	"unicode"
	"unsafe"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/iavl"
	idb "github.com/cosmos/iavl/db"
	"github.com/cosmos/iavl/hash"
	"github.com/cosmos/iavl/keyformat"
)

// storeMeta is one IAVL store in application.db.
// Mounted follows the v6.4 program: dest names and the IBC names load.
// The migratable names stay in the file and are not mounted.
type storeMeta struct {
	Name    string `json:"name"`
	Prefix  string `json:"prefix"`
	Hasher  string `json:"hasher"`
	Mounted bool   `json:"mounted"`
	Present bool   `json:"present"`
	Latest  int64  `json:"latest_block"`
	Error   string `json:"error,omitempty"`
}

type record struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	NodeHeight   int    `json:"node_height"`
	Leaves       int64  `json:"leaves"`
	SavedInBlock int64  `json:"saved_in_block"`
	KeyHex       string `json:"key_hex"`
	KeyText      string `json:"key_text"`
	ValueHex     string `json:"value_hex,omitempty"`
	ValueText    string `json:"value_text,omitempty"`
	ValueLen     int    `json:"value_len"`
	HashHex      string `json:"hash_hex"`
	Left         string `json:"left,omitempty"`
	Right        string `json:"right,omitempty"`
	Missing      bool   `json:"missing,omitempty"`
	Error        string `json:"error,omitempty"`
}

type nodeView struct {
	Store storeMeta `json:"store"`
	Block int64     `json:"block"`
	Reads int       `json:"reads"`
	Node  *record   `json:"node,omitempty"`
	Left  *record   `json:"left,omitempty"`
	Right *record   `json:"right,omitempty"`
	Error string    `json:"error,omitempty"`
}

// branch is one real node plus the children this read actually loaded.
// Depth is capped. Unread levels stay off this object.
type branch struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	NodeHeight int     `json:"node_height"`
	Leaves     int64   `json:"leaves"`
	KeyHex     string  `json:"key_hex"`
	KeyText    string  `json:"key_text"`
	Missing    bool    `json:"missing,omitempty"`
	Left       *branch `json:"left,omitempty"`
	Right      *branch `json:"right,omitempty"`
}

type treeView struct {
	nodeView
	Depth int     `json:"depth"`
	Tree  *branch `json:"tree,omitempty"`
}

type explorer struct {
	db    dbm.DB
	mu    sync.Mutex
	trees map[string]*iavl.MutableTree
	list  []storeMeta
}

func newExplorer(db dbm.DB) *explorer {
	return &explorer{db: db, trees: map[string]*iavl.MutableTree{}}
}

func catalog() []storeMeta {
	var out []storeMeta
	add := func(name string, mounted bool) {
		out = append(out, storeMeta{
			Name:    name,
			Prefix:  "s/k:" + name + "/",
			Hasher:  algorithmName(name),
			Mounted: mounted,
		})
	}
	for _, name := range []string{
		"upgrade", "ibc", "transfer", "08-wasm", "icahost", "icacontroller",
		"packetfowardmiddleware", "params",
	} {
		add(name, true)
	}
	for _, name := range destStores() {
		add(name, true)
	}
	for _, name := range migratableStores() {
		add(name, false)
	}
	return out
}

func (e *explorer) Stores() []storeMeta {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.list == nil {
		list := catalog()
		for i := range list {
			latest, ok, err := latestBlock(e.db, []byte(list[i].Prefix))
			list[i].Present = ok
			list[i].Latest = latest
			if err != nil {
				list[i].Error = err.Error()
			}
		}
		e.list = list
	}
	return append([]storeMeta(nil), e.list...)
}

func (e *explorer) meta(name string) (storeMeta, bool) {
	for _, s := range catalog() {
		if s.Name == name {
			for _, probed := range e.Stores() {
				if probed.Name == name {
					return probed, true
				}
			}
		}
	}
	return storeMeta{}, false
}

// Tree reads the focus node and its descendants down to depth levels.
// depth 6 is at most 127 records. It does not walk the rest of the store.
func (e *explorer) Tree(name string, block int64, idHex string, depth int) treeView {
	if depth < 1 {
		depth = 1
	}
	if depth > 6 {
		depth = 6
	}
	meta, ok := e.meta(name)
	out := treeView{nodeView: nodeView{Store: meta}, Depth: depth}
	if !ok {
		out.Error = "Unknown store."
		return out
	}
	if block == 0 {
		block = meta.Latest
	}
	out.Block = block
	if idHex != "" && idHex != "root" {
		if _, err := hex.DecodeString(idHex); err != nil {
			out.Error = "The node id is not hex."
			return out
		}
	} else if !meta.Present {
		out.Error = "This store has no saved block in this database."
		return out
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	prefix := dbm.NewPrefixDB(e.db, []byte(meta.Prefix))
	var node *iavl.Node
	reads := 0
	if idHex == "" || idHex == "root" {
		tree, err := e.tree(meta.Name, prefix)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		if _, err = tree.LoadVersion(block); err != nil {
			out.Error = err.Error()
			return out
		}
		rootVal := reflect.ValueOf(tree).Elem().FieldByName("root")
		if !rootVal.IsValid() || rootVal.IsNil() {
			out.Error = "This block has an empty root."
			return out
		}
		node = (*iavl.Node)(unsafe.Pointer(rootVal.Pointer()))
		reads = 1
	} else {
		id, _ := hex.DecodeString(idHex)
		var err error
		node, err = loadNode(prefix, meta.Name, id)
		reads = 1
		if err != nil {
			out.Error = err.Error()
			return out
		}
	}
	b := e.grow(prefix, meta.Name, node, depth, &reads)
	rec := readRecord(node)
	out.Node = &rec
	out.Tree = b
	out.Reads = reads
	if b != nil && b.Left != nil {
		out.Left = branchRecord(b.Left)
	}
	if b != nil && b.Right != nil {
		out.Right = branchRecord(b.Right)
	}
	return out
}

func (e *explorer) grow(prefix dbm.DB, name string, node *iavl.Node, depth int, reads *int) *branch {
	rec := readRecord(node)
	b := branchFrom(rec)
	if depth <= 1 || rec.Kind == "leaf" {
		return b
	}
	if rec.Left != "" {
		id, err := hex.DecodeString(rec.Left)
		if err != nil {
			b.Left = &branch{ID: rec.Left, Missing: true}
		} else {
			child, err := loadNode(prefix, name, id)
			*reads++
			if err != nil {
				b.Left = &branch{ID: rec.Left, Missing: true}
			} else {
				b.Left = e.grow(prefix, name, child, depth-1, reads)
			}
		}
	}
	if rec.Right != "" {
		id, err := hex.DecodeString(rec.Right)
		if err != nil {
			b.Right = &branch{ID: rec.Right, Missing: true}
		} else {
			child, err := loadNode(prefix, name, id)
			*reads++
			if err != nil {
				b.Right = &branch{ID: rec.Right, Missing: true}
			} else {
				b.Right = e.grow(prefix, name, child, depth-1, reads)
			}
		}
	}
	return b
}

func branchFrom(rec record) *branch {
	return &branch{
		ID:         rec.ID,
		Kind:       rec.Kind,
		NodeHeight: rec.NodeHeight,
		Leaves:     rec.Leaves,
		KeyHex:     rec.KeyHex,
		KeyText:    rec.KeyText,
	}
}

func branchRecord(b *branch) *record {
	return &record{
		ID:         b.ID,
		Kind:       b.Kind,
		NodeHeight: b.NodeHeight,
		Leaves:     b.Leaves,
		KeyHex:     b.KeyHex,
		KeyText:    b.KeyText,
		Missing:    b.Missing,
	}
}

func (e *explorer) Node(name string, block int64, idHex string) nodeView {
	meta, ok := e.meta(name)
	view := nodeView{Store: meta}
	if !ok {
		view.Error = "Unknown store."
		return view
	}
	if block == 0 {
		block = meta.Latest
	}
	view.Block = block
	if idHex == "" || idHex == "root" {
		if !meta.Present {
			view.Error = "This store has no saved block in this database."
			return view
		}
		return e.root(meta, block)
	}
	id, err := hex.DecodeString(idHex)
	if err != nil {
		view.Error = "The node id is not hex."
		return view
	}
	return e.focus(meta, block, id)
}

func (e *explorer) root(meta storeMeta, block int64) nodeView {
	view := nodeView{Store: meta, Block: block}
	e.mu.Lock()
	defer e.mu.Unlock()
	prefix := dbm.NewPrefixDB(e.db, []byte(meta.Prefix))
	tree, err := e.tree(meta.Name, prefix)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	if _, err := tree.LoadVersion(block); err != nil {
		view.Error = err.Error()
		return view
	}
	rootVal := reflect.ValueOf(tree).Elem().FieldByName("root")
	if !rootVal.IsValid() || rootVal.IsNil() {
		view.Error = "This block has an empty root."
		return view
	}
	node := (*iavl.Node)(unsafe.Pointer(rootVal.Pointer()))
	view.Reads = 1
	rec := readRecord(node)
	view.Node = &rec
	e.attachChildren(&view, prefix, meta.Name, node)
	return view
}

func (e *explorer) focus(meta storeMeta, block int64, id []byte) nodeView {
	view := nodeView{Store: meta, Block: block}
	e.mu.Lock()
	defer e.mu.Unlock()
	prefix := dbm.NewPrefixDB(e.db, []byte(meta.Prefix))
	node, err := loadNode(prefix, meta.Name, id)
	view.Reads = 1
	if err != nil {
		view.Error = err.Error()
		return view
	}
	rec := readRecord(node)
	view.Node = &rec
	e.attachChildren(&view, prefix, meta.Name, node)
	return view
}

func (e *explorer) attachChildren(view *nodeView, prefix dbm.DB, name string, node *iavl.Node) {
	leftID := bytesField(node, "leftNodeKey")
	rightID := bytesField(node, "rightNodeKey")
	if len(leftID) > 0 {
		child, err := loadNode(prefix, name, leftID)
		view.Reads++
		if err != nil {
			view.Left = &record{ID: hex.EncodeToString(leftID), Missing: true, Error: err.Error()}
		} else {
			rec := readRecord(child)
			view.Left = &rec
		}
	}
	if len(rightID) > 0 {
		child, err := loadNode(prefix, name, rightID)
		view.Reads++
		if err != nil {
			view.Right = &record{ID: hex.EncodeToString(rightID), Missing: true, Error: err.Error()}
		} else {
			rec := readRecord(child)
			view.Right = &rec
		}
	}
}

func (e *explorer) tree(name string, prefix dbm.DB) (*iavl.MutableTree, error) {
	if tree, ok := e.trees[name]; ok {
		return tree, nil
	}
	tree := iavl.NewMutableTree(idb.NewWrapper(prefix), 256, true, iavl.NewNopLogger(), iavl.HasherOptionForStore(name))
	e.trees[name] = tree
	return tree, nil
}

func loadNode(db dbm.DB, store string, id []byte) (*iavl.Node, error) {
	buf, err := db.Get(storageKey(id))
	if err != nil {
		return nil, err
	}
	if buf == nil && len(id) == 12 && binary.BigEndian.Uint32(id[8:]) == 1 {
		alt := append([]byte(nil), id...)
		binary.BigEndian.PutUint32(alt[8:], 0)
		buf, err = db.Get(storageKey(alt))
		if err != nil {
			return nil, err
		}
		if buf != nil {
			id = alt
		}
	}
	if buf == nil {
		return nil, fmt.Errorf("no record for node %x", id)
	}
	return iavl.MakeNode(id, buf, hasherFor(store))
}

func hasherFor(store string) hash.Hasher {
	return iavl.HasherForStore(store)
}

func storageKey(id []byte) []byte {
	if len(id) == 32 {
		out := make([]byte, 33)
		out[0] = 'n'
		copy(out[1:], id)
		return out
	}
	out := make([]byte, 1+len(id))
	out[0] = 's'
	copy(out[1:], id)
	return out
}

func readRecord(node *iavl.Node) record {
	key := bytesField(node, "key")
	val := bytesField(node, "value")
	sum := bytesField(node, "hash")
	left := bytesField(node, "leftNodeKey")
	right := bytesField(node, "rightNodeKey")
	nv := reflect.ValueOf(node).Elem()
	height := int(nv.FieldByName("subtreeHeight").Int())
	leaves := nv.FieldByName("size").Int()
	kind := "inner node"
	if height == 0 {
		kind = "leaf"
	}
	id := node.GetKey()
	saved := int64(0)
	if len(id) >= 8 && len(id) != 32 {
		saved = int64(binary.BigEndian.Uint64(id[:8]))
	}
	rec := record{
		ID:           hex.EncodeToString(id),
		Kind:         kind,
		NodeHeight:   height,
		Leaves:       leaves,
		SavedInBlock: saved,
		KeyHex:       hex.EncodeToString(key),
		KeyText:      textOf(key),
		ValueLen:     len(val),
		HashHex:      hex.EncodeToString(sum),
	}
	if kind == "leaf" {
		rec.ValueHex = hex.EncodeToString(truncate(val, 96))
		rec.ValueText = textOf(truncate(val, 96))
	}
	if len(left) > 0 {
		rec.Left = hex.EncodeToString(left)
	}
	if len(right) > 0 {
		rec.Right = hex.EncodeToString(right)
	}
	return rec
}

func bytesField(node *iavl.Node, name string) []byte {
	v := reflect.ValueOf(node).Elem().FieldByName(name)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.IsNil() {
		return nil
	}
	n := v.Len()
	if n == 0 {
		return []byte{}
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(v.Pointer())), n)
	out := make([]byte, n)
	copy(out, src)
	return out
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

func textOf(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	for _, r := range string(b) {
		if r == unicode.ReplacementChar || !unicode.IsPrint(r) {
			return ""
		}
	}
	return string(b)
}

var versionPrefix = keyformat.NewFastPrefixFormatter('s', 8)

func latestBlock(db dbm.DB, prefix []byte) (int64, bool, error) {
	start := append(append([]byte{}, prefix...), versionPrefix.KeyInt64(1)...)
	end := append(append([]byte{}, prefix...), versionPrefix.KeyInt64(math.MaxInt64)...)
	itr, err := db.ReverseIterator(start, end)
	if err != nil {
		return 0, false, err
	}
	defer itr.Close()
	if !itr.Valid() {
		return 0, false, itr.Error()
	}
	k := itr.Key()
	off := len(prefix) + 1
	if len(k) < off+8 {
		return 0, false, fmt.Errorf("short node key")
	}
	return int64(binary.BigEndian.Uint64(k[off : off+8])), true, nil
}
