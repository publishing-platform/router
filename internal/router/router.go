package router

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/publishing-platform/router/internal/handlers"
	"github.com/publishing-platform/router/internal/triemux"
)

// Router is a wrapper around an HTTP multiplexer (trie.Mux)
type Router struct {
	mux        *triemux.Mux
	lock       sync.RWMutex
	opts       Options
	ReloadChan chan bool
	Logger     zerolog.Logger
}

type Options struct {
	DatabaseURL          string
	DatabaseName         string
	Listener             *pq.Listener
	DatabasePollInterval time.Duration
	BackendConnTimeout   time.Duration
	BackendHeaderTimeout time.Duration
	Logger               zerolog.Logger
}

type Route struct {
	IncomingPath sql.NullString
	RouteType    sql.NullString
	Handler      sql.NullString
	Disabled     bool
	BackendID    sql.NullString
	RedirectTo   sql.NullString
	RedirectType sql.NullString
	SegmentsMode sql.NullString
}

func NewRouter(o Options) (rt *Router, err error) {
	o.Logger.Info().Msgf("using database poll interval: %v", o.DatabasePollInterval)
	o.Logger.Info().Msgf("using backend connect timeout: %v", o.BackendConnTimeout)
	o.Logger.Info().Msgf("using backend header timeout: %v", o.BackendHeaderTimeout)

	listenerProblemReporter := func(event pq.ListenerEventType, err error) {
		if err != nil {
			o.Logger.Error().Err(err).Msg("error creating listener for PSQL notify channel")
			return
		}
	}

	listener := pq.NewListener(o.DatabaseURL, 10*time.Second, time.Minute, listenerProblemReporter)
	o.Listener = listener

	err = listener.Listen("notify")
	if err != nil {
		panic(err)
	}

	rt = &Router{
		mux:        triemux.NewMux(o.Logger),
		opts:       o,
		ReloadChan: make(chan bool, 1),
		Logger:     o.Logger,
	}

	go rt.pollAndReload()

	return rt, nil
}

// ServeHTTP delegates responsibility for serving requests to the proxy mux
// instance for this router.
func (rt *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			rt.Logger.Err(fmt.Errorf("%v", r)).Msgf("recovered from panic in ServeHTTP")

			w.WriteHeader(http.StatusInternalServerError)
		}
	}()

	rt.lock.RLock()
	mux := rt.mux
	rt.lock.RUnlock()

	mux.ServeHTTP(w, req)
}

func (rt *Router) SelfUpdateRoutes() {
	rt.Logger.Info().Msgf("starting self-update process, polling for route changes every: %v", rt.opts.DatabasePollInterval)

	tick := time.Tick(rt.opts.DatabasePollInterval)
	for range tick {
		rt.Logger.Info().Msg("polling db for changes")

		rt.ReloadChan <- true
	}
}

// pollAndReload blocks until it receives a message on reloadChan,
// and will immediately reload again if another message was received
// during reload.
func (rt *Router) pollAndReload() {
	for range rt.ReloadChan {
		func() {
			defer func() {
				if r := recover(); r != nil {
					rt.Logger.Err(fmt.Errorf("%v", r)).Msgf("recovered from panic in pollAndReload")
				}
			}()

			rt.Logger.Info().Msgf("connecting to: %v", rt.opts.DatabaseURL)

			db, err := sql.Open("postgres", rt.opts.DatabaseURL)
			if err != nil {
				rt.Logger.Error().Err(err).Msg("error connecting to PSQL database, skipping update")
				return
			}

			defer db.Close()

			if rt.shouldReload(rt.opts.Listener) {
				rt.Logger.Info().Msg("updates found")
				rt.reloadRoutes(db)
			} else {
				rt.Logger.Info().Msg("no updates found")
			}
		}()
	}
}

func (rt *Router) shouldReload(listener *pq.Listener) bool {
	// we assume a route count of zero means router startup
	if rt.mux.RouteCount() == 0 {
		return true
	}

	select {
	case n := <-listener.Notify:
		// n.Extra contains the payload from the notification
		rt.Logger.Info().Msgf("notification:: %v", n.Channel)
		return true
	default:
		if err := listener.Ping(); err != nil {
			panic(err)
		}
		return false
	}
}

// reloadRoutes reloads the routes for this Router instance on the fly. It will
// create a new proxy mux, load applications (backends) and routes into it, and
// then flip the "mux" pointer in the Router.
func (rt *Router) reloadRoutes(db *sql.DB) {

	defer func() {
		if r := recover(); r != nil {
			rt.Logger.Err(fmt.Errorf("%v", r)).Msgf("recovered from panic in reloadRoutes")
			rt.Logger.Info().Msg("original routes have not been modified")
		}
	}()

	rt.Logger.Info().Msg("reloading routes")

	newmux := triemux.NewMux(rt.Logger)

	backends := rt.loadBackendsFromEnv()
	loadRoutes(db, newmux, backends, rt.Logger)
	routeCount := newmux.RouteCount()

	rt.lock.Lock()
	rt.mux = newmux
	rt.lock.Unlock()

	rt.Logger.Info().Int("route_count", routeCount).Msg("reloaded routes")
}

func (rt *Router) loadBackendsFromEnv() (backends map[string]http.Handler) {
	backends = make(map[string]http.Handler)

	for _, envvar := range os.Environ() {
		pair := strings.SplitN(envvar, "=", 2)

		if !strings.HasPrefix(pair[0], "BACKEND_URL_") {
			continue
		}

		backendID := strings.TrimPrefix(pair[0], "BACKEND_URL_")
		backendURL := pair[1]

		if backendURL == "" {
			rt.Logger.Warn().Msgf("no URL for backend %s provided, skipping", backendID)
			continue
		}

		backend, err := url.Parse(backendURL)
		if err != nil {
			rt.Logger.Warn().Err(err).Msgf("failed to parse URL %s for backend %s, skipping", backendURL, backendID)
			continue
		}

		backends[backendID] = handlers.NewBackendHandler(
			backendID,
			backend,
			rt.opts.BackendConnTimeout,
			rt.opts.BackendHeaderTimeout,
			rt.Logger,
		)
	}

	return
}
