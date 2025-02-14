package router

import (
	"database/sql"
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

// loadRoutes is a helper function which loads routes from the passed database
// and registers them with the passed proxy mux.
func loadRoutes(db *sql.DB, mux *triemux.Mux, backends map[string]http.Handler, logger zerolog.Logger) error {
	route := &Route{}

	rows, err := db.Query("SELECT incoming_path, route_type, handler, disabled, backend_id, redirect_to, redirect_type, segments_mode FROM routes")
	if err != nil {
		logger.Error().Err(err).Msg("error retrieving row information from routes database table, skipping update.")
		return err
	}

	for rows.Next() {
		err := rows.Scan(&route.IncomingPath, &route.RouteType, &route.Handler, &route.Disabled, &route.BackendID, &route.RedirectTo, &route.RedirectType, &route.SegmentsMode)
		if err != nil {
			logger.Error().Err(err).Msg("error retrieving row information from routes database table, skipping update.")
			return err
		}

		err = addHandler(mux, route, backends, logger)

		if err != nil {
			return err
		}
	}

	return nil
}

func addHandler(mux *triemux.Mux, route *Route, backends map[string]http.Handler, logger zerolog.Logger) error {
	// if route.IncomingPath == nil || route.RouteType == nil {
	// 	logger.Warn().Interface("route", route).Msg("ignoring route with nil fields")
	// 	return nil
	// }

	prefix := (route.RouteType.String == RouteTypePrefix)

	// the database contains paths with % encoded routes.
	// Unescape them here because the http.Request objects we match against contain the unescaped variants.
	incomingURL, err := url.Parse(route.IncomingPath.String)
	if err != nil {
		logger.Warn().Interface("route", route).Str("incoming_path", route.IncomingPath.String).Msg("ignoring route with invalid incoming path")
		return nil //nolint:nilerr
	}

	if route.Disabled {
		mux.Handle(incomingURL.Path, prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
		}))

		logger.Info().Msgf("registered %s (prefix: %v)(disabled) -> Unavailable", incomingURL.Path, prefix)
		return nil //nolint:nilerr
	}

	switch route.Handler.String {
	case HandlerTypeBackend:
		handler, ok := backends[route.BackendID.String]
		if !ok {
			logger.Warn().Str("incoming_path", route.IncomingPath.String).Str("backend_id", route.BackendID.String).Msg("ignoring route with unknown backend")
			return nil
		}
		mux.Handle(incomingURL.Path, prefix, handler)
		logger.Info().Msgf("registered %s (prefix: %v) for %s", incomingURL.Path, prefix, route.BackendID.String)
	case HandlerTypeRedirect:
		redirectTemporarily := (route.RedirectType.String == "temporary")
		handler := handlers.NewRedirectHandler(incomingURL.Path, route.RedirectTo.String, shouldPreserveSegments(route), redirectTemporarily, logger)
		mux.Handle(incomingURL.Path, prefix, handler)
		logger.Info().Msgf("registered %s (prefix: %v) -> %s", incomingURL.Path, prefix, route.RedirectTo.String)
	case HandlerTypeGone:
		mux.Handle(incomingURL.Path, prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "410 Gone", http.StatusGone)
		}))
		logger.Info().Msgf("registered %s (prefix: %v) -> Gone", incomingURL.Path, prefix)
	default:
		logger.Warn().Interface("route", route).Str("handler_type", route.Handler.String).Msg("ignoring route with unknown handler type")
	}

	return nil
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
