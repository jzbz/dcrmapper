package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jzbz/dcrmapper/crawler"
)

var amgr *crawler.Manager
var domain string

const timeFormat = "02 Jan 2006 15:04 MST"

// assetVersions caches a content hash per static asset path so the asset() URLs
// only change when the file's contents change. Computed lazily and cached for
// the life of the process (assets do not change without a restart).
var (
	assetMu       sync.Mutex
	assetVersions = map[string]string{}
)

// assetURL appends a short content-hash query string to a static asset path so
// browsers (and the long-lived CDN/proxy cache) re-fetch it whenever it changes,
// while still caching aggressively between changes. webPath is the public URL
// path, e.g. "/public/css/tailwind.css".
func assetURL(webPath string) string {
	assetMu.Lock()
	defer assetMu.Unlock()

	if v, ok := assetVersions[webPath]; ok {
		return webPath + "?v=" + v
	}

	v := "dev"
	if b, err := os.ReadFile("." + webPath); err == nil {
		sum := sha256.Sum256(b)
		v = hex.EncodeToString(sum[:])[:10]
	} else {
		log.Printf("asset %s: could not hash for cache-busting: %v", webPath, err)
	}
	assetVersions[webPath] = v
	return webPath + "?v=" + v
}

func NewRouter() *gin.Engine {
	// With release mode enabled, gin will only read template files once and cache them.
	// With release mode disabled, templates will be reloaded on the fly.
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	// Recovery middleware handles any go panics generated while processing web
	// requests. Ensures a 500 response is sent to the client rather than
	// sending no response at all.
	router.Use(gin.Recovery())

	router.SetFuncMap(template.FuncMap{
		"incr":  func(i int) int { return i + 1 },
		"date":  func(t time.Time) string { return t.In(time.UTC).Format(timeFormat) },
		"asset": assetURL,
	})

	router.Static("/public", "./public/")
	router.LoadHTMLGlob("templates/*")

	// Page routes.
	router.GET("/", homepage)
	router.GET("/all_nodes", list)
	router.GET("/user_agents", userAgents)
	router.GET("/node", node)

	// Page API routes.
	router.GET("/world_nodes", worldNodes)
	router.GET("/nodes", paginatedNodes)

	// Data API routes.
	router.GET("/api/user_agents", api)

	return router
}

func Start(ctx context.Context, listen string, cookieDomain string, mgr *crawler.Manager, requestShutdownChan chan struct{}, shutdownWg *sync.WaitGroup) error {
	amgr = mgr
	domain = cookieDomain

	// Create TCP listener.
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", listen)
	if err != nil {
		return err
	}
	log.Printf("Listening on %s", listen)

	srv := http.Server{
		Handler:      NewRouter(),
		ReadTimeout:  5 * time.Second,  // slow requests should not hold connections opened
		WriteTimeout: 60 * time.Second, // hung responses must die
	}

	// Add the graceful shutdown to the waitgroup.
	shutdownWg.Add(1)
	go func() {
		// Wait until shutdown is signaled before shutting down.
		<-ctx.Done()

		// Give the webserver 10 seconds to finish what it is doing.
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(timeoutCtx); err != nil {
			log.Printf("Failed to stop webserver cleanly: %v", err)
		} else {
			log.Printf("Webserver stopped")
		}
		shutdownWg.Done()
	}()

	// Start webserver.
	go func() {
		// If the server dies for any reason other than ErrServerClosed (from
		// graceful server.Shutdown), log the error and request shutdown.
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Unexpected webserver error: %v", err)
			requestShutdownChan <- struct{}{}
		}
	}()

	return nil
}
