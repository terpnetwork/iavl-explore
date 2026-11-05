package main

import (
	"errors"
	"reflect"
	"unsafe"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

var errReadOnly = errors.New("iavl-explore is read-only")

// openDB opens application.db for reads. It does not take the LOCK file.
// LevelDB read-only mode rejects Put and Delete.
func openDB(path string) (dbm.DB, error) {
	stor, err := openReadOnlyStorage(path)
	if err != nil {
		return nil, err
	}
	ldb, err := leveldb.Open(stor, &opt.Options{
		ReadOnly:               true,
		ErrorIfMissing:         true,
		OpenFilesCacheCapacity: 16,
	})
	if err != nil {
		stor.Close()
		return nil, err
	}
	return injectLevelDB(ldb), nil
}

// injectLevelDB places ldb into cosmos-db's GoLevelDB. The field is not
// exported, and GoLevelDB has no constructor that accepts an open DB.
func injectLevelDB(ldb *leveldb.DB) *dbm.GoLevelDB {
	g := &dbm.GoLevelDB{}
	field := reflect.ValueOf(g).Elem().FieldByName("db")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(ldb))
	return g
}
