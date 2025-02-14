package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	cacheDuration = 30 * time.Minute
)

func addCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Expires", time.Now().Add(cacheDuration).Format(time.RFC1123))
	w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d, public", cacheDuration/time.Second))
}

type redirectHandler struct {
	url    string
	code   int
	logger zerolog.Logger
}

type pathPreservingRedirectHandler struct {
	sourcePrefix string
	targetPrefix string
	code         int
	logger       zerolog.Logger
}

func NewRedirectHandler(source, target string, preserve bool, temporary bool, logger zerolog.Logger) http.Handler {
	statusMoved := http.StatusMovedPermanently
	if temporary {
		statusMoved = http.StatusFound
	}
	if preserve {
		return &pathPreservingRedirectHandler{source, target, statusMoved, logger}
	}
	return &redirectHandler{target, statusMoved, logger}
}

func (handler *redirectHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	addCacheHeaders(writer)

	http.Redirect(writer, request, handler.url, handler.code)
}

func (handler *pathPreservingRedirectHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	target := handler.targetPrefix + strings.TrimPrefix(request.URL.Path, handler.sourcePrefix)
	if request.URL.RawQuery != "" {
		target += "?" + request.URL.RawQuery
	}

	addCacheHeaders(writer)
	http.Redirect(writer, request, target, handler.code)
}

type downcaseRedirectHandler struct{}

func NewDowncaseRedirectHandler() http.Handler {
	return &downcaseRedirectHandler{}
}

func (handler *downcaseRedirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const status = http.StatusMovedPermanently

	target := strings.ToLower(r.URL.Path)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	addCacheHeaders(w)
	http.Redirect(w, r, target, status)
}
