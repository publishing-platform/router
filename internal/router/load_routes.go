package router

import (
	"context"
	"net/http"
	"net/url"

	"github.com/publishing-platform/router/internal/handlers"
	"github.com/publishing-platform/router/internal/triemux"
	"github.com/rs/zerolog"
)

const (
	RouteTypePrefix      = "prefix"
	RouteTypeExact       = "exact"
	SegmentsModePreserve = "preserve"
	SegmentsModeIgnore   = "ignore"

	HandlerTypeBackend  = "backend"
	HandlerTypeRedirect = "redirect"
	HandlerTypeGone     = "gone"
)

// loadRoutes is a helper function which loads routes from the passed database connection pool
// and registers them with the passed proxy mux.
func loadRoutes(pool PgxIface, mux *triemux.Mux, backends map[string]http.Handler, logger zerolog.Logger) error {
	rows, err := pool.Query(context.Background(),
		"SELECT incoming_path, route_type, handler, disabled, backend_id, redirect_to, redirect_type, segments_mode FROM routes")

	if err != nil {
		return err
	}

	defer rows.Close()

	for rows.Next() {
		route := &Route{}
		scans := []any{
			&route.IncomingPath,
			&route.RouteType,
			&route.Handler,
			&route.Disabled,
			&route.BackendID,
			&route.RedirectTo,
			&route.RedirectType,
			&route.SegmentsMode,
		}

		err := rows.Scan(scans...)
		if err != nil {
			return err
		}

		err = addHandler(mux, route, backends, logger)
		if err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func addHandler(mux *triemux.Mux, route *Route, backends map[string]http.Handler, logger zerolog.Logger) error {
	if route.IncomingPath == nil || route.RouteType == nil {
		logger.Warn().Interface("route", route).Msg("ignoring route with nil fields")
		return nil
	}

	prefix := (*route.RouteType == RouteTypePrefix)

	// the database contains paths with % encoded routes.
	// Unescape them here because the http.Request objects we match against contain the unescaped variants.
	incomingURL, err := url.Parse(*route.IncomingPath)
	if err != nil {
		logger.Warn().Interface("route", route).Str("incoming_path", *route.IncomingPath).Msg("ignoring route with invalid incoming path")
		return nil //nolint:nilerr
	}

	if *route.Disabled {
		mux.Handle(incomingURL.Path, prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
		}))

		logger.Info().Msgf("registered %s (prefix: %v)(disabled) -> Unavailable", incomingURL.Path, prefix)
		return nil //nolint:nilerr
	}

	switch *route.Handler {
	case HandlerTypeBackend:
		handler, ok := backends[*route.BackendID]
		if !ok {
			logger.Warn().Str("incoming_path", *route.IncomingPath).Str("backend_id", *route.BackendID).Msg("ignoring route with unknown backend")
			return nil
		}
		mux.Handle(incomingURL.Path, prefix, handler)
		logger.Info().Msgf("registered %s (prefix: %v) for %v", incomingURL.Path, prefix, *route.BackendID)
	case HandlerTypeRedirect:
		redirectTemporarily := (*route.RedirectType == "temporary")
		handler := handlers.NewRedirectHandler(incomingURL.Path, *route.RedirectTo, shouldPreserveSegments(route), redirectTemporarily, logger)
		mux.Handle(incomingURL.Path, prefix, handler)
		logger.Info().Msgf("registered %s (prefix: %v) -> %v", incomingURL.Path, prefix, *route.RedirectTo)
	case HandlerTypeGone:
		mux.Handle(incomingURL.Path, prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "410 Gone", http.StatusGone)
		}))
		logger.Info().Msgf("registered %s (prefix: %v) -> Gone", incomingURL.Path, prefix)
	default:
		logger.Warn().Interface("route", route).Str("handler_type", *route.Handler).Msg("ignoring route with unknown handler type")
	}

	return nil
}

func shouldPreserveSegments(route *Route) bool {
	switch *route.RouteType {
	case RouteTypeExact:
		return route.SegmentsMode != nil && *route.SegmentsMode == SegmentsModePreserve
	case RouteTypePrefix:
		return route.SegmentsMode == nil || (*route.SegmentsMode != SegmentsModeIgnore)
	default:
		return false
	}
}
