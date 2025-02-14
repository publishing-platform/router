package router

import (
	"fmt"
	"net/http"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pashagolub/pgxmock/v4"
)

var _ = Describe("Router", func() {
	Describe("reloadRoutes", func() {
		var (
			mockPool pgxmock.PgxPoolIface
			router   *Router
		)

		BeforeEach(func() {
			var err error
			mockPool, err = pgxmock.NewPool()
			Expect(err).NotTo(HaveOccurred())

			router = &Router{
				lock: sync.RWMutex{},
				backends: map[string]http.Handler{
					"backend1": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, "http://example.com", http.StatusFound)
					}),
					"backend2": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, "http://example.com", http.StatusFound)
					}),
				},
			}
		})

		AfterEach(func() {
			mockPool.Close()
		})

		It("should reload routes from content store successfully", func() {
			rows := pgxmock.NewRows([]string{"incoming_path", "route_type", "handler", "disabled", "backend_id", "redirect_to", "redirect_type", "segments_mode"}).
				AddRow(stringPtr("/path1"), stringPtr("exact"), stringPtr("backend"), boolPtr(false), stringPtr("backend1"), nil, nil, nil).
				AddRow(stringPtr("/path2"), stringPtr("prefix"), stringPtr("backend"), boolPtr(false), stringPtr("backend2"), nil, nil, nil)

			mockPool.ExpectQuery("SELECT").WillReturnRows(rows)

			router.reloadRoutes(mockPool)

			Expect(router.mux.RouteCount()).To(Equal(2))
		})

		It("should handle panic and log error", func() {
			defer GinkgoRecover()

			mockPool.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("some error"))

			Expect(func() { router.reloadRoutes(mockPool) }).NotTo(Panic())
		})
	})
})
