package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	_ "embed"
)

//go:embed web/index.html
var indexHTML []byte

func main() {
	dbPath := flag.String("db", "", "path to application.db (read-only, no LOCK)")
	addr := flag.String("addr", "127.0.0.1:8780", "listen address")
	webDir := flag.String("web", "", "directory containing index.html; empty serves the embedded page")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: iavl-explore -db /path/to/application.db [-addr 127.0.0.1:8780] [-web dir]")
		os.Exit(2)
	}

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatalf("open %s: %v", *dbPath, err)
	}
	ex := newExplorer(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		servePage(w, r, *webDir)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"ok":        true,
			"db":        *dbPath,
			"read_only": true,
		})
	})
	mux.HandleFunc("/api/stores", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, ex.Stores())
	})
	mux.HandleFunc("/api/node", func(w http.ResponseWriter, r *http.Request) {
		block, ok := queryBlock(r)
		if !ok {
			writeJSON(w, nodeView{Error: "The block is not an integer."})
			return
		}
		writeJSON(w, ex.Node(r.URL.Query().Get("store"), block, r.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/tree", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		block, ok := queryBlock(r)
		if !ok {
			writeJSON(w, treeView{nodeView: nodeView{Error: "The block is not an integer."}})
			return
		}
		depth, err := strconv.Atoi(q.Get("depth"))
		if q.Get("depth") == "" {
			depth = 6
			err = nil
		}
		if err != nil {
			writeJSON(w, treeView{nodeView: nodeView{Error: "The depth is not an integer."}})
			return
		}
		writeJSON(w, ex.Tree(q.Get("store"), block, q.Get("id"), depth))
	})

	log.Printf("iavl-explore read-only %s on %s", *dbPath, *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func servePage(w http.ResponseWriter, r *http.Request, webDir string) {
	if webDir != "" {
		http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func queryBlock(r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("version")
	if raw == "" {
		return 0, true
	}
	block, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return block, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}
