package router

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/publishing-platform/router/internal/handlers"
	"github.com/publishing-platform/router/internal/logger"
	"github.com/publishing-platform/router/internal/triemux"
)

const (
	RouteTypePrefix      = "prefix"
	RouteTypeExact       = "exact"
	SegmentsModePreserve = "preserve"
	SegmentsModeIgnore   = "ignore"
)

// Router is a wrapper around an HTTP multiplexer (trie.Mux)
type Router struct {
	mux        *triemux.Mux
	lock       sync.RWMutex
	logger     logger.Logger
	opts       Options
	ReloadChan chan bool
}

type Options struct {
	DatabaseURL          string
	DatabaseName         string
	Listener             *pq.Listener
	DatabasePollInterval time.Duration
	BackendConnTimeout   time.Duration
	BackendHeaderTimeout time.Duration
	LogFileName          string
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
	log.Println("router: using database poll interval:", o.DatabasePollInterval)
	log.Println("router: using backend connect timeout:", o.BackendConnTimeout)
	log.Println("router: using backend header timeout:", o.BackendHeaderTimeout)

	l, err := logger.New(o.LogFileName)
	if err != nil {
		return nil, err
	}

	log.Println("router: logging errors as JSON to", o.LogFileName)

	listenerProblemReporter := func(event pq.ListenerEventType, err error) {
		if err != nil {
			log.Println(fmt.Sprintf("pq: error creating listener for PSQL notify channel: %v)", err))
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
		mux:        triemux.NewMux(),
		logger:     l,
		opts:       o,
		ReloadChan: make(chan bool, 1),
	}

	go rt.pollAndReload()

	return rt, nil
}

// ServeHTTP delegates responsibility for serving requests to the proxy mux
// instance for this router.
func (rt *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("router: recovered from panic in ServeHTTP:", r)

			errorMessage := fmt.Sprintf("panic: %v", r)
			// err := logger.RecoveredError{ErrorMessage: errorMessage}

			// logger.NotifySentry(logger.ReportableError{Error: err, Request: req})
			rt.logger.LogFromClientRequest(map[string]interface{}{
				"error":  errorMessage,
				"status": http.StatusInternalServerError,
			}, req)

			w.WriteHeader(http.StatusInternalServerError)
		}
	}()

	rt.lock.RLock()
	mux := rt.mux
	rt.lock.RUnlock()

	mux.ServeHTTP(w, req)
}

func (rt *Router) SelfUpdateRoutes() {
	log.Println("router: starting self-update process, polling for route changes every", rt.opts.DatabasePollInterval)

	tick := time.Tick(rt.opts.DatabasePollInterval)
	for range tick {
		log.Println("router: polling db for changes")

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
					log.Println(r)
				}
			}()

			log.Println("pq: connecting to", rt.opts.DatabaseURL)

			db, err := sql.Open("postgres", rt.opts.DatabaseURL)
			if err != nil {
				log.Println(fmt.Sprintf("pq: error connecting to PSQL database, skipping update (error: %v)", err))
				return
			}

			defer db.Close()

			if rt.shouldReload(rt.opts.Listener) {
				log.Println("router: updates found")
				rt.reloadRoutes(db)
			} else {
				log.Println("router: no updates found - really?")
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
		log.Println("notification:", n.Channel)
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
			log.Println("router: recovered from panic in reloadRoutes:", r)
			log.Println("router: original routes have not been modified")
			// errorMessage := fmt.Sprintf("panic: %v", r)
			// err := logger.RecoveredError{ErrorMessage: errorMessage}
			// logger.NotifySentry(logger.ReportableError{Error: err}) // TODO
		}
	}()

	log.Println("router: reloading routes")

	newmux := triemux.NewMux()

	backends := rt.loadBackendsFromEnv()
	loadRoutes(db, newmux, backends)
	routeCount := newmux.RouteCount()

	rt.lock.Lock()
	rt.mux = newmux
	rt.lock.Unlock()

	log.Println(fmt.Sprintf("router: reloaded %d routes", routeCount))
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
			log.Println(fmt.Sprintf("no URL for backend %s provided, skipping)", backendID))
			// logger.Warn().Msgf("no URL for backend %s provided, skipping", backendID)
			continue
		}

		backend, err := url.Parse(backendURL)
		if err != nil {
			log.Println(fmt.Sprintf("failed to parse URL %s for backend %s, skipping", backendURL, backendID))
			// logger.Warn().Err(err).Msgf("failed to parse URL %s for backend %s, skipping", backendURL, backendID)
			continue
		}

		backends[backendID] = handlers.NewBackendHandler(
			backendID,
			backend,
			rt.opts.BackendConnTimeout,
			rt.opts.BackendHeaderTimeout,
			rt.logger,
		)
	}

	return
}

// loadRoutes is a helper function which loads routes from the passed database
// and registers them with the passed proxy mux.
func loadRoutes(db *sql.DB, mux *triemux.Mux, backends map[string]http.Handler) {
	route := &Route{}

	rows, err := db.Query("SELECT incoming_path, route_type, handler, disabled, backend_id, redirect_to, redirect_type, segments_mode FROM routes")
	if err != nil {
		log.Println(fmt.Sprintf("pq: error retrieving row information from table, skipping update. (error: %v)", err))
		return
	}

	goneHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "410 Gone", http.StatusGone)
	})
	unavailableHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
	})

	for rows.Next() {
		err := rows.Scan(&route.IncomingPath, &route.RouteType, &route.Handler, &route.Disabled, &route.BackendID, &route.RedirectTo, &route.RedirectType, &route.SegmentsMode)
		if err != nil {
			log.Println(fmt.Sprintf("pq: error retrieving row information from table, skipping update. (error: %v)", err))
			return
		}

		prefix := (route.RouteType.String == RouteTypePrefix)

		// the database contains paths with % encoded routes.
		// Unescape them here because the http.Request objects we match against contain the unescaped variants.
		incomingURL, err := url.Parse(route.IncomingPath.String)
		if err != nil {
			log.Println(fmt.Sprintf("router: found route %+v with invalid incoming path '%s', skipping!", route, route.IncomingPath.String))
			continue
		}

		if route.Disabled {
			mux.Handle(incomingURL.Path, prefix, unavailableHandler)
			log.Println(fmt.Sprintf("router: registered %s (prefix: %v)(disabled) -> Unavailable", incomingURL.Path, prefix))
			continue
		}

		switch route.Handler.String {
		case "backend":
			handler, ok := backends[route.BackendID.String]
			if !ok {
				log.Println(fmt.Sprintf("router: found route %+v which references unknown backend "+
					"%s, skipping!", route, route.BackendID.String))
				continue
			}
			mux.Handle(incomingURL.Path, prefix, handler)
			log.Println(fmt.Sprintf("router: registered %s (prefix: %v) for %s",
				incomingURL.Path, prefix, route.BackendID.String))
		case "redirect":
			redirectTemporarily := (route.RedirectType.String == "temporary")
			handler := handlers.NewRedirectHandler(incomingURL.Path, route.RedirectTo.String, shouldPreserveSegments(route), redirectTemporarily)
			mux.Handle(incomingURL.Path, prefix, handler)
			log.Println(fmt.Sprintf("router: registered %s (prefix: %v) -> %s",
				incomingURL.Path, prefix, route.RedirectTo.String))
		case "gone":
			mux.Handle(incomingURL.Path, prefix, goneHandler)
			log.Println(fmt.Sprintf("router: registered %s (prefix: %v) -> Gone", incomingURL.Path, prefix))
		case "boom":
			// Special handler so that we can test failure behaviour.
			mux.Handle(incomingURL.Path, prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("Boom!!!")
			}))
			log.Println(fmt.Sprintf("router: registered %s (prefix: %v) -> Boom!!!", incomingURL.Path, prefix))
		default:
			log.Println(fmt.Sprintf("router: found route %+v with unknown handler type "+
				"%s, skipping!", route, route.Handler.String))
			continue
		}
	}
}

func shouldPreserveSegments(route *Route) bool {
	switch route.RouteType.String {
	case RouteTypeExact:
		return route.SegmentsMode.String == SegmentsModePreserve
	case RouteTypePrefix:
		return route.SegmentsMode.String != SegmentsModeIgnore
	default:
		return false
	}
}
