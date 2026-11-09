package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (m *Manager) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := m.document(id); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m.status(id))
	})
	mux.HandleFunc("POST /v1/documents/{id}/process", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-DuChinese-Local") != "1" {
			http.Error(w, "local client required", 403)
			return
		}
		status, err := m.start(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("POST /v1/documents/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-DuChinese-Local") != "1" {
			http.Error(w, "local client required", 403)
			return
		}
		status, err := m.stop(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("GET /v1/documents/{id}/pages/{page}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !documentID.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		page, err := strconv.Atoi(r.PathValue("page"))
		s := m.status(id)
		if err != nil || page < 1 || page > s.Total || s.State == "stale" || s.Hash == "" {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(m.Cache, id, s.Hash, fmt.Sprintf("page-%03d.json", page))
		data, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "null" && !strings.HasPrefix(origin, "qrc:") {
			http.Error(w, "local client required", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func main() {
	model.ConfigPath = "disable"
	home, _ := os.UserHomeDir()
	address := flag.String("listen", "127.0.0.1:9876", "loopback address")
	documents := flag.String("documents", filepath.Join(home, ".local/share/remarkable/xochitl"), "original PDF directory")
	cache := flag.String("cache", filepath.Join(home, ".local/share/duchinese-pdf-processing"), "lookup cache")
	keys := flag.String("credentials", filepath.Join(home, ".config/duchinese-pdf-processing/credentials.json"), "private API credential file")
	parallelPages := flag.Int("parallel-pages", defaultParallelPages, "maximum pages processed at once (1-20)")
	flag.Parse()
	if *parallelPages < 1 || *parallelPages > maxParallelPages {
		log.Fatal("parallel-pages must be between 1 and 20")
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("processor must listen on loopback")
	}
	info, err := os.Stat(*keys)
	if err != nil {
		log.Fatal("processor credentials are missing")
	}
	if info.Mode().Perm()&0077 != 0 {
		log.Fatal("processor credentials must have mode 0600")
	}
	data, err := os.ReadFile(*keys)
	if err != nil {
		log.Fatal("cannot read processor credentials")
	}
	var credentials Credentials
	if json.Unmarshal(data, &credentials) != nil || credentials.Vision == "" || credentials.DeepSeek == "" {
		log.Fatal("invalid processor credential configuration")
	}
	manager, err := newManager(*documents, *cache, newCloud(credentials))
	if err != nil {
		log.Fatal(err)
	}
	manager.parallelPages = *parallelPages
	server := &http.Server{Addr: *address, Handler: manager.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	log.Printf("Chinese PDF processor listening on %s", *address)
	log.Fatal(server.ListenAndServe())
}
