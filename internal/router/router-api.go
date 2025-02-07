package router

import (
	"net/http"
)

func NewAPIHandler(rout *Router) (api http.Handler, err error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Send a message to the Router goroutine which will start a reload if necessary.
		// If the channel is already full, no message will be sent and the request
		// won't be blocked.
		select {
		case rout.ReloadChan <- true:
		default:
		}
		rout.Logger.Info().Msg("reload queued")
		w.WriteHeader(http.StatusAccepted)
		_, err := w.Write([]byte("Reload queued"))
		if err != nil {
			rout.Logger.Warn().Err(err).Msg("failed to write response")
		}
	})

	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		_, err := w.Write([]byte("OK"))
		if err != nil {
			rout.Logger.Warn().Err(err).Msg("failed to write response")
		}
	})

	return mux, nil
}
