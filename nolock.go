package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/syndtr/goleveldb/leveldb/storage"
)

// roStorage reads a live LevelDB directory without the LOCK file.
// The node already holds an exclusive lock. A shared lock fails with
// "resource temporarily unavailable". This storage does not create or
// rename files. Read-only mode must not call Create, Remove, or SetMeta.
type roStorage struct {
	path string
	mu   sync.Mutex
	open int
}

func openReadOnlyStorage(path string) (*roStorage, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}
	return &roStorage{path: path, open: 1}, nil
}

type noopLock struct{}

func (noopLock) Unlock() {}

func (s *roStorage) Lock() (storage.Locker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open < 0 {
		return nil, storage.ErrClosed
	}
	return noopLock{}, nil
}

func (s *roStorage) Log(string) {}

func (s *roStorage) SetMeta(storage.FileDesc) error { return errReadOnly }

func (s *roStorage) Create(storage.FileDesc) (storage.Writer, error) { return nil, errReadOnly }

func (s *roStorage) Remove(storage.FileDesc) error { return errReadOnly }

func (s *roStorage) Rename(storage.FileDesc, storage.FileDesc) error { return errReadOnly }

func (s *roStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open = -1
	return nil
}

func (s *roStorage) GetMeta() (storage.FileDesc, error) {
	b, err := os.ReadFile(filepath.Join(s.path, "CURRENT"))
	if err != nil {
		return storage.FileDesc{}, err
	}
	line := strings.TrimRight(string(b), "\r\n")
	fd, ok := parseFile(line)
	if !ok {
		return storage.FileDesc{}, fmt.Errorf("CURRENT has no manifest name: %q", line)
	}
	if _, err := os.Stat(filepath.Join(s.path, fd.String())); err != nil {
		return storage.FileDesc{}, err
	}
	return fd, nil
}

func (s *roStorage) List(ft storage.FileType) ([]storage.FileDesc, error) {
	entries, err := os.ReadDir(s.path)
	if err != nil {
		return nil, err
	}
	var out []storage.FileDesc
	for _, e := range entries {
		fd, ok := parseFile(e.Name())
		if ok && fd.Type&ft != 0 {
			out = append(out, fd)
		}
	}
	return out, nil
}

func (s *roStorage) Open(fd storage.FileDesc) (storage.Reader, error) {
	if !storage.FileDescOk(fd) {
		return nil, storage.ErrInvalidFile
	}
	f, err := os.Open(filepath.Join(s.path, fd.String()))
	if err != nil {
		old := oldTableName(fd)
		if old != "" && os.IsNotExist(err) {
			f, err = os.Open(filepath.Join(s.path, old))
		}
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func oldTableName(fd storage.FileDesc) string {
	if fd.Type != storage.TypeTable {
		return ""
	}
	return fmt.Sprintf("%06d.sst", fd.Num)
}

func parseFile(name string) (storage.FileDesc, bool) {
	var fd storage.FileDesc
	var tail string
	if _, err := fmt.Sscanf(name, "%d.%s", &fd.Num, &tail); err == nil {
		switch tail {
		case "log":
			fd.Type = storage.TypeJournal
		case "ldb", "sst":
			fd.Type = storage.TypeTable
		case "tmp":
			fd.Type = storage.TypeTemp
		default:
			return storage.FileDesc{}, false
		}
		return fd, true
	}
	if n, _ := fmt.Sscanf(name, "MANIFEST-%d%s", &fd.Num, &tail); n == 1 {
		fd.Type = storage.TypeManifest
		return fd, true
	}
	return storage.FileDesc{}, false
}
