package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/iavl"
	idb "github.com/cosmos/iavl/db"
	"github.com/syndtr/goleveldb/leveldb"
)

func TestRootAndChild(t *testing.T) {
	raw := dbm.NewMemDB()
	prefix := dbm.NewPrefixDB(raw, []byte("s/k:bank/"))
	tree := iavl.NewMutableTree(idb.NewWrapper(prefix), 0, true, iavl.NewNopLogger(), iavl.HasherOptionForStore("bank"))
	for _, kv := range [][2]string{{"alice", "1"}, {"bob", "22"}, {"carol", "333"}} {
		if _, err := tree.Set([]byte(kv[0]), []byte(kv[1])); err != nil {
			t.Fatal(err)
		}
	}
	_, ver, err := tree.SaveVersion()
	if err != nil {
		t.Fatal(err)
	}

	ex := newExplorer(raw)
	stores := ex.Stores()
	var bank, dest storeMeta
	for _, s := range stores {
		switch s.Name {
		case "bank":
			bank = s
		case "b3-bank":
			dest = s
		}
	}
	if !bank.Present || bank.Latest != ver || bank.Mounted || bank.Hasher != "sha256" {
		t.Fatalf("bank catalog: %+v", bank)
	}
	if dest.Present || !dest.Mounted || dest.Hasher != "blake3" {
		t.Fatalf("b3-bank catalog: %+v", dest)
	}

	view := ex.Node("bank", ver, "root")
	if view.Error != "" || view.Node == nil {
		t.Fatalf("root: %+v", view)
	}
	if view.Node.Leaves != 3 || view.Node.Kind != "inner node" {
		t.Fatalf("root record: %+v", view.Node)
	}
	if view.Left == nil || view.Right == nil {
		t.Fatalf("children missing: %+v", view)
	}
	if view.Reads != 3 {
		t.Fatalf("reads = %d, want 3", view.Reads)
	}
	if view.Left.Leaves+view.Right.Leaves != 3 {
		t.Fatalf("child leaves %d + %d", view.Left.Leaves, view.Right.Leaves)
	}

	leaf := view.Left
	if leaf.Kind != "leaf" {
		leaf = view.Right
	}
	if leaf.Kind != "leaf" {
		child := ex.Node("bank", ver, view.Left.ID)
		if child.Error != "" || child.Node == nil {
			t.Fatalf("descend: %+v", child)
		}
		if child.Left != nil && child.Left.Kind == "leaf" {
			leaf = child.Left
		} else if child.Right != nil && child.Right.Kind == "leaf" {
			leaf = child.Right
		} else if child.Node.Kind == "leaf" {
			leaf = child.Node
		}
	}
	switch leaf.KeyText {
	case "alice", "bob", "carol":
	default:
		t.Fatalf("leaf key %q hex %s", leaf.KeyText, leaf.KeyHex)
	}
	if leaf.ValueLen == 0 || leaf.ValueText == "" {
		t.Fatalf("leaf value: %+v", leaf)
	}

	miss := ex.Node("b3-bank", ver, "root")
	if miss.Error == "" {
		t.Fatalf("expected missing dest tree, got %+v", miss.Node)
	}

	tv := ex.Tree("bank", ver, "root", 6)
	if tv.Error != "" || tv.Tree == nil || tv.Tree.Leaves != 3 {
		t.Fatalf("tree: %+v", tv)
	}
	if tv.Depth != 6 {
		t.Fatalf("depth %d", tv.Depth)
	}
	if tv.Reads < 3 || tv.Reads > 15 {
		t.Fatalf("tree reads %d", tv.Reads)
	}
	if !hasLeaf(tv.Tree) {
		t.Fatal("tree did not reach a leaf")
	}
}

func hasLeaf(b *branch) bool {
	if b == nil {
		return false
	}
	if b.Kind == "leaf" {
		return true
	}
	return hasLeaf(b.Left) || hasLeaf(b.Right)
}

func TestReadOnlyOpenRejectsWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "application.db")
	ldb, err := leveldb.OpenFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ldb.Put([]byte("k"), []byte("v"), nil); err != nil {
		t.Fatal(err)
	}
	if err := ldb.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Get([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Fatalf("got %q", got)
	}
	if err := db.Set([]byte("k"), []byte("nope")); err == nil {
		t.Fatal("read-only Set succeeded")
	}
	got, err = db.Get([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Fatalf("value changed to %q", got)
	}
}

func TestPageSameOrigin(t *testing.T) {
	if bytes.Contains(indexHTML, []byte("/iavl-api")) {
		t.Fatal("page still calls /iavl-api")
	}
	if !bytes.Contains(indexHTML, []byte(`const API = "";`)) {
		t.Fatal("page is not same-origin")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	servePage(rr, req, "")
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), indexHTML) {
		t.Fatalf("page status %d len %d", rr.Code, rr.Body.Len())
	}
}
