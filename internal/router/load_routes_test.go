package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/publishing-platform/router/internal/triemux"
	"github.com/rs/zerolog"
)

var _ = Describe("loadRoutes", func() {
	var (
		mockPool pgxmock.PgxPoolIface
		mux      *triemux.Mux
		backends map[string]http.Handler
		logger   zerolog.Logger
	)

	BeforeEach(func() {
		var err error
		mockPool, err = pgxmock.NewPool()
		Expect(err).NotTo(HaveOccurred())

		logger := zerolog.New(os.Stdout)

		mux = triemux.NewMux(logger)
		backends = map[string]http.Handler{
			"backend1": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("backend1")); err != nil {
					fmt.Println("Failed to write to the response", err)
				}
			}),
			"backend2": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("backend2")); err != nil {
					fmt.Println("Failed to write to the response", err)
				}
			}),
			"frontend": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("frontend")); err != nil {
					fmt.Println("Failed to write to the response", err)
				}
			}),
		}
	})

	AfterEach(func() {
		mockPool.Close()
	})

	Context("when database has backend routes", func() {
		BeforeEach(func() {
			rows := pgxmock.NewRows([]string{"incoming_path", "route_type", "handler", "disabled", "backend_id", "redirect_to", "redirect_type", "segments_mode"}).
				AddRow(stringPtr("/path1"), stringPtr("exact"), stringPtr("backend"), boolPtr(false), stringPtr("backend1"), nil, nil, nil).
				AddRow(stringPtr("/path2"), stringPtr("prefix"), stringPtr("backend"), boolPtr(false), stringPtr("backend2"), nil, nil, nil)

			mockPool.ExpectQuery("SELECT").WillReturnRows(rows)

			err := loadRoutes(mockPool, mux, backends, logger)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should load backend exact routes correctly", func() {
			req, _ := http.NewRequest(http.MethodGet, "/path1", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusOK))
			Expect(rr.Body.String()).To(Equal("backend1"))
		})

		It("should load backend prefix routes correctly", func() {
			req, _ := http.NewRequest(http.MethodGet, "/path2/foo/bar", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusOK))
			Expect(rr.Body.String()).To(Equal("backend2"))
		})
	})

	Context("when database has gone routes", func() {
		BeforeEach(func() {
			rows := pgxmock.NewRows([]string{"incoming_path", "route_type", "handler", "disabled", "backend_id", "redirect_to", "redirect_type", "segments_mode"}).
				AddRow(stringPtr("/frontend-gone"), stringPtr("exact"), stringPtr("gone"), boolPtr(false), nil, nil, nil, nil).
				AddRow(stringPtr("/path2"), stringPtr("prefix"), stringPtr("gone"), boolPtr(false), stringPtr("backend2"), nil, nil, nil)

			mockPool.ExpectQuery("SELECT").WillReturnRows(rows)

			err := loadRoutes(mockPool, mux, backends, logger)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should load gone route correctly", func() {
			req, _ := http.NewRequest(http.MethodGet, "/frontend-gone", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusGone))
		})
	})

	Context("when content store has redirect routes", func() {
		BeforeEach(func() {
			rows := pgxmock.NewRows([]string{"incoming_path", "route_type", "handler", "disabled", "backend_id", "redirect_to", "redirect_type", "segments_mode"}).
				AddRow(stringPtr("/redirect-exact"), stringPtr("exact"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-exact"), stringPtr("permanent"), nil).
				AddRow(stringPtr("/redirect-prefix"), stringPtr("prefix"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-prefix"), stringPtr("permanent"), nil).
				AddRow(stringPtr("/redirect-exact-ignore"), stringPtr("exact"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-exact-ignore"), stringPtr("permanent"), stringPtr("ignore")).
				AddRow(stringPtr("/redirect-exact-nil-ignore"), stringPtr("exact"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-exact-nil-ignore"), stringPtr("permanent"), nil).
				AddRow(stringPtr("/redirect-prefix-ignore"), stringPtr("prefix"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-prefix-ignore"), stringPtr("permanent"), stringPtr("ignore")).
				AddRow(stringPtr("/redirect-exact-preserve"), stringPtr("exact"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-exact-preserve"), stringPtr("permanent"), stringPtr("preserve")).
				AddRow(stringPtr("/redirect-prefix-preserve"), stringPtr("prefix"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-prefix-preserve"), stringPtr("permanent"), stringPtr("preserve")).
				AddRow(stringPtr("/redirect-prefix-nil-preserve"), stringPtr("prefix"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-prefix-nil-preserve"), stringPtr("permanent"), nil).
				AddRow(stringPtr("/redirect-temporary-exact"), stringPtr("exact"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-temporary-exact"), stringPtr("temporary"), nil).
				AddRow(stringPtr("/redirect-temporary-prefix"), stringPtr("prefix"), stringPtr("redirect"), boolPtr(false), nil, stringPtr("/redirected-temporary-prefix"), stringPtr("temporary"), nil)

			mockPool.ExpectQuery("SELECT").WillReturnRows(rows)

			err := loadRoutes(mockPool, mux, backends, logger)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should load exact redirect route", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-exact", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-exact"))
		})

		It("should load prefix redirect route", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-prefix/foo/bar", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-prefix/foo/bar"))
		})

		It("should load exact redirect route that ignores suffix segments", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-exact-ignore", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-exact-ignore"))
		})

		It("should load exact redirect route that ignores suffix segments if segments mode is nil", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-exact-nil-ignore", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-exact-nil-ignore"))
		})

		It("should load prefix redirect route that ignores suffix segments", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-prefix-ignore/foo/bar", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-prefix-ignore"))
		})

		It("should load exact redirect route that preserves suffix segments", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-exact-preserve", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-exact-preserve"))
		})

		It("should load prefix redirect route that preserves suffix segments", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-prefix-preserve/foo/bar", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-prefix-preserve/foo/bar"))
		})

		It("should load prefix redirect route that preserves suffix segments if segments mode is nil", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-prefix-nil-preserve/foo/bar", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusMovedPermanently))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-prefix-nil-preserve/foo/bar"))
		})

		It("should load exact temporary redirect route", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-temporary-exact", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusFound))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-temporary-exact"))
		})

		It("should load prefix temporary redirect route", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-temporary-exact", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusFound))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-temporary-exact"))
		})

		It("should load prefix temporary redirect route", func() {
			req, _ := http.NewRequest(http.MethodGet, "/redirect-temporary-prefix/foo/bar", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			Expect(rr.Code).To(Equal(http.StatusFound))
			Expect(rr.Header().Get("Location")).To(Equal("/redirected-temporary-prefix/foo/bar"))
		})
	})
})

func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}
