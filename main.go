package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/publishing-platform/router/internal/handlers"
	"github.com/publishing-platform/router/internal/router"
)

func getenv(key string, defaultVal string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return defaultVal
}

func getenvDuration(key string, defaultVal string) time.Duration {
	s := getenv(key, defaultVal)
	return mustParseDuration(s)
}

func mustParseDuration(s string) (d time.Duration) {
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Fatal(err)
	}
	return
}

func listenAndServeOrFatal(addr string, handler http.Handler, rTimeout time.Duration, wTimeout time.Duration) {
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  rTimeout,
		WriteTimeout: wTimeout,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func main() {
	fmt.Println("Publishing Platform Router")

	var (
		pubAddr = getenv("ROUTER_PUBADDR", ":8080")
		apiAddr         = getenv("ROUTER_APIADDR", ":8081")
		databaseURL     = getenv("DATABASE_URL", "postgresql://postgres@127.0.0.1:5432/router_development?sslmode=disable")
		databaseName    = getenv("DATABASE_NAME", "router_development")
		dbPollInterval  = getenvDuration("ROUTER_POLL_INTERVAL", "2s")
		errorLogFile    = getenv("ROUTER_ERROR_LOG", "STDERR")
		tlsSkipVerify   = os.Getenv("ROUTER_TLS_SKIP_VERIFY") != ""
		beConnTimeout   = getenvDuration("ROUTER_BACKEND_CONNECT_TIMEOUT", "1s")
		beHeaderTimeout = getenvDuration("ROUTER_BACKEND_HEADER_TIMEOUT", "20s")
		feReadTimeout   = getenvDuration("ROUTER_FRONTEND_READ_TIMEOUT", "60s")
		feWriteTimeout  = getenvDuration("ROUTER_FRONTEND_WRITE_TIMEOUT", "60s")
	)

	if tlsSkipVerify {
		handlers.TLSSkipVerify = true
		log.Printf("skipping verification of TLS certificates; " +
			"Do not use this option in a production environment.")
	}

	rout, err := router.NewRouter(router.Options{
		DatabaseURL:          databaseURL,
		DatabaseName:         databaseName,
		DatabasePollInterval: dbPollInterval,
		BackendConnTimeout:   beConnTimeout,
		BackendHeaderTimeout: beHeaderTimeout,
		LogFileName:          errorLogFile,
	})
	if err != nil {
		log.Fatal(err)
	}

	// DEBGUGGING
	// ======================================================
	// t := trie.NewTrie[interface{}]()
	// t.Set([]string{"foo", "bar"}, 123)
	// v, ok := t.Get([]string{"foo"})

	// fmt.Println(v)
	// fmt.Println(ok)

	// v, ok = t.Get([]string{"foo", "bar"})

	// fmt.Println(v)
	// fmt.Println(ok)

	// ok = t.Del([]string{"foo", "bar"})

	// fmt.Println(ok)

	// v, ok = t.Get([]string{"foo", "bar"})

	// fmt.Println(v)
	// fmt.Println(ok)
	// ======================================================

	go rout.SelfUpdateRoutes()

	go listenAndServeOrFatal(pubAddr, rout, feReadTimeout, feWriteTimeout)
	log.Printf("router: listening for requests on %v", pubAddr)

	api, err := router.NewAPIHandler(rout)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("router: listening for API requests on %v", apiAddr)
	listenAndServeOrFatal(apiAddr, api, feReadTimeout, feWriteTimeout)	
}
