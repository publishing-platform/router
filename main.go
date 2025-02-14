package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/getsentry/sentry-go"
	sentryzerolog "github.com/getsentry/sentry-go/zerolog"
	"github.com/rs/zerolog"

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
	fmt.Printf("Publishing Platform Router: %s\n", router.VersionInfo())

	// Initialize Sentry
	if err := sentry.Init(sentry.ClientOptions{}); err != nil {
		panic(err)
	}

	defer sentry.Flush(2 * time.Second)

	// Configure Sentry Zerolog Writer
	writer, err := sentryzerolog.New(sentryzerolog.Config{
		ClientOptions: sentry.ClientOptions{},
		Options: sentryzerolog.Options{
			Levels:          []zerolog.Level{zerolog.ErrorLevel, zerolog.FatalLevel},
			FlushTimeout:    3 * time.Second,
			WithBreadcrumbs: true,
		},
	})
	if err != nil {
		panic(err)
	}
	defer writer.Close()

	// Initialize Zerolog
	m := zerolog.MultiLevelWriter(os.Stderr, writer)
	logger := zerolog.New(m).With().Timestamp().Logger()

	var (
		pubAddr             = getenv("ROUTER_PUBADDR", ":8080")
		apiAddr             = getenv("ROUTER_APIADDR", ":8081")
		databaseURL         = getenv("DATABASE_URL", "postgresql://postgres@127.0.0.1:5432/router_development?sslmode=disable")
		tlsSkipVerify       = os.Getenv("ROUTER_TLS_SKIP_VERIFY") != ""
		beConnTimeout       = getenvDuration("ROUTER_BACKEND_CONNECT_TIMEOUT", "1s")
		beHeaderTimeout     = getenvDuration("ROUTER_BACKEND_HEADER_TIMEOUT", "20s")
		feReadTimeout       = getenvDuration("ROUTER_FRONTEND_READ_TIMEOUT", "60s")
		feWriteTimeout      = getenvDuration("ROUTER_FRONTEND_WRITE_TIMEOUT", "60s")
		routeReloadInterval = getenvDuration("ROUTER_ROUTE_RELOAD_INTERVAL", "1m")
	)

	logger.Info().Msgf("frontend read timeout: %v", feReadTimeout)
	logger.Info().Msgf("frontend write timeout: %v", feWriteTimeout)
	logger.Info().Msgf("GOMAXPROCS value of %d", runtime.GOMAXPROCS(0))

	if tlsSkipVerify {
		handlers.TLSSkipVerify = true
		logger.Warn().Msg("skipping verification of TLS certificates; Do not use this option in a production environment.")
	}

	rout, err := router.NewRouter(router.Options{
		DatabaseURL:          databaseURL,
		RouteReloadInterval:  routeReloadInterval,
		BackendConnTimeout:   beConnTimeout,
		BackendHeaderTimeout: beHeaderTimeout,
		Logger:               logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create router")
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

	go rout.PeriodicRouteUpdates()

	go listenAndServeOrFatal(pubAddr, rout, feReadTimeout, feWriteTimeout)
	logger.Info().Msgf("listening for requests on %v", pubAddr)

	api, err := router.NewAPIHandler(rout)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create API handler")
	}
	logger.Info().Msgf("listening for API requests on %v", apiAddr)
	listenAndServeOrFatal(apiAddr, api, feReadTimeout, feWriteTimeout)
}
