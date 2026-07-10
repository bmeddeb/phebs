// Command phebs is the self-hosted code-search server: API, UI, sync, and
// indexing in one binary.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/bmeddeb/phebs/ui"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: phebs serve [-addr :3070]")
		os.Exit(2)
	}
	serve(os.Args[2:])
}

// ponytail: stdlib mux stub; huma API (T1.4), config (T1.1), and store (T1.2)
// replace this as Epic 1 lands.
func serve(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", ":3070", "listen address")
	_ = flags.Parse(args)

	dist, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(dist))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
	})
	log.Printf("phebs skeleton listening on %s (embedded storage lands in T1.2)", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
