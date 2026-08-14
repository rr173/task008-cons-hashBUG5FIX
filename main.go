package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	smoke := flag.Bool("smoke-test", false, "run a built-in self-test and exit")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	if *smoke {
		if err := runSmokeTest(); err != nil {
			fmt.Fprintln(os.Stderr, "smoke-test FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("smoke-test PASSED")
		return
	}

	srv := NewService()
	mux := buildMux(srv)
	log.Printf("conshash listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func buildMux(srv *Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /nodes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name         string `json:"name"`
			VirtualNodes *int   `json:"virtualNodes"` // pointer: omitted => default
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, badRequest("invalid JSON body: %v", err))
			return
		}
		vn := 0
		if req.VirtualNodes != nil {
			vn = *req.VirtualNodes
		}
		node, err := srv.AddNode(req.Name, vn)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, node)
	})

	mux.HandleFunc("GET /nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"nodes": srv.ListNodes()})
	})

	mux.HandleFunc("GET /nodes/{name}", func(w http.ResponseWriter, r *http.Request) {
		node, err := srv.GetNode(r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	})

	mux.HandleFunc("DELETE /nodes/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := srv.RemoveNode(r.PathValue("name")); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	})

	mux.HandleFunc("GET /owner", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if strings.TrimSpace(key) == "" {
			writeError(w, badRequest("query parameter 'key' must not be empty"))
			return
		}
		owner, err := srv.Owner(key)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "owner": owner})
	})

	mux.HandleFunc("POST /owners", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Keys []string `json:"keys"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, badRequest("invalid JSON body: %v", err))
			return
		}
		if len(req.Keys) == 0 {
			writeError(w, badRequest("'keys' must not be empty"))
			return
		}
		owners, err := srv.Owners(req.Keys)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"owners": owners})
	})

	return mux
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	if e, ok := err.(*statusErr); ok {
		writeJSON(w, e.code, map[string]any{"error": e.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
}
