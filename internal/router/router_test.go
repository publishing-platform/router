package router

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
)

var _ = Describe("Router", func() {
	Describe("loadBackendsFromEnv", func() {
		var (
			router *Router
			logger = zerolog.New(os.Stdout)
		)

		BeforeEach(func() {
			router = &Router{
				opts: Options{
					BackendConnTimeout:   1 * time.Second,
					BackendHeaderTimeout: 20 * time.Second,
					Logger:               logger,
				},
			}
		})

		It("should load backends from environment variables", func() {
			os.Setenv("BACKEND_URL_testBackend", "http://example.com")
			defer os.Unsetenv("BACKEND_URL_testBackend")

			backends := router.loadBackendsFromEnv()

			Expect(backends).To(HaveKey("testBackend"))
			Expect(backends["testBackend"]).ToNot(BeNil())
		})

		It("should skip backends with empty URLs", func() {
			os.Setenv("BACKEND_URL_emptyBackend", "")
			defer os.Unsetenv("BACKEND_URL_emptyBackend")

			backends := router.loadBackendsFromEnv()

			Expect(backends).ToNot(HaveKey("emptyBackend"))
		})

		It("should skip backends with invalid URLs", func() {
			os.Setenv("BACKEND_URL_invalidBackend", "://invalid-url")
			defer os.Unsetenv("BACKEND_URL_invalidBackend")

			backends := router.loadBackendsFromEnv()

			Expect(backends).ToNot(HaveKey("invalidBackend"))
		})
	})
})
