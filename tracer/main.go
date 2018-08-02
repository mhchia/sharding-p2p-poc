package tracer

import (
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"sourcegraph.com/sourcegraph/appdash"
	"sourcegraph.com/sourcegraph/appdash/traceapp"
)

func main() {
	memStore := appdash.NewMemoryStore()
	store := &appdash.RecentStore{
		MinEvictAge: 20 * time.Second,
		DeleteStore: memStore,
	}

	// Set up web UI server
	url, err := url.Parse("http://localhost:8700")
	if err != nil {
		log.Fatal(err)
	}
	tapp, err := traceapp.New(nil, url)
	if err != nil {
		log.Fatal(err)
	}
	tapp.Store = store
	tapp.Queryer = memStore
	log.Println("Appdash web UI running on HTTP :8700")
	go func() {
		log.Fatal(http.ListenAndServe(":8700", tapp))
	}()

	// Set up Collector Server
	ln, err := net.Listen("tcp", ":8701")
	if err != nil {
		log.Fatal(err)
	}
	collector := appdash.NewLocalCollector(store)
	cs := appdash.NewServer(ln, collector)
	log.Println("Appdash remote collector running on HTTP :8701")
	cs.Start()
}
