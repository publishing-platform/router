package router

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgxlisten"
	"github.com/rs/zerolog"

	"github.com/publishing-platform/router/internal/triemux"
)

type PgxIface interface {
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
}

// Router is a wrapper around an HTTP multiplexer (trie.Mux)
type Router struct {
	backends              map[string]http.Handler
	mux                   *triemux.Mux
	lock                  sync.RWMutex
	opts                  Options
	ReloadChan            chan bool
	pool                  *pgxpool.Pool
	lastAttemptReloadTime time.Time
	Logger                zerolog.Logger
}

type Options struct {
	DatabaseURL          string
	RouteReloadInterval  time.Duration
	BackendConnTimeout   time.Duration
	BackendHeaderTimeout time.Duration
	Logger               zerolog.Logger
}

type Route struct {
	IncomingPath *string
	RouteType    *string
	Handler      *string
	Disabled     *bool
	BackendID    *string
	RedirectTo   *string
	RedirectType *string
	SegmentsMode *string
}

func NewRouter(o Options) (rt *Router, err error) {
	backends := loadBackendsFromEnv(o.BackendConnTimeout, o.BackendHeaderTimeout, o.Logger)

	var pool *pgxpool.Pool

	pool, err = pgxpool.New(context.Background(), o.DatabaseURL)
	if err != nil {
		return nil, err
	}
	o.Logger.Info().Msg("postgres connection pool created")

	rt = &Router{
		backends:   backends,
		mux:        triemux.NewMux(o.Logger),
		opts:       o,
		ReloadChan: make(chan bool, 1),
		pool:       pool,
		Logger:     o.Logger,
	}

	// load routes on startup
	rt.reloadRoutes(pool)

	go func() {
		if err := rt.listenForUpdates(context.Background()); err != nil {
			rt.Logger.Error().Err(err).Msg("failed to listen for database updates")
		}
	}()

	go rt.waitForReload()

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

func (rt *Router) listenForUpdates(ctx context.Context) error {
	listener := &pgxlisten.Listener{
		Connect: func(ctx context.Context) (*pgx.Conn, error) {
			c, err := rt.pool.Acquire(ctx)
			if err != nil {
				return nil, err
			}
			return c.Conn(), nil
		},
	}

	listener.Handle(
		"notify",
		pgxlisten.HandlerFunc(
			func(ctx context.Context, notification *pgconn.Notification, conn *pgx.Conn) error {
				// This is a non-blocking send, if there is already a notification to reload we don't need to send another one
				select {
				case rt.ReloadChan <- true:
				default:
				}
				return nil
			},
		),
	)

	err := listener.Listen(ctx)

	if err != nil {
		return err
	}

	return nil
}

func (rt *Router) waitForReload() {
	for range rt.ReloadChan {
		rt.reloadRoutes(rt.pool)
	}
}

func (rt *Router) PeriodicRouteUpdates() {
	tick := time.Tick(5 * time.Second)
	for range tick {
		if time.Since(rt.lastAttemptReloadTime) > rt.opts.RouteReloadInterval {
			// This is a non-blocking send, if there is already a notification to reload we don't need to send another one
			select {
			case rt.ReloadChan <- true:
			default:
			}
		}
	}
}

// reloadRoutes reloads the routes for this Router instance on the fly. It will
// create a new proxy mux, load applications (backends) and routes into it, and
// then flip the "mux" pointer in the Router.
func (rt *Router) reloadRoutes(pool PgxIface) {

	defer func() {
		if r := recover(); r != nil {
			rt.Logger.Err(fmt.Errorf("%v", r)).Msgf("recovered from panic in reloadRoutes")
			rt.Logger.Info().Msg("reload failed and existing routes have not been modified")
		}
	}()

	rt.lastAttemptReloadTime = time.Now()

	rt.Logger.Info().Msg("reloading routes from database")
	newmux := triemux.NewMux(rt.Logger)

	err := loadRoutes(pool, newmux, rt.backends, rt.Logger)
	if err != nil {
		rt.Logger.Warn().Err(err).Msg("error reloading routes")
		return
	}

	routeCount := newmux.RouteCount()

	rt.lock.Lock()
	rt.mux = newmux
	rt.lock.Unlock()

	rt.Logger.Info().Int("route_count", routeCount).Msg("reloaded routes")
}
