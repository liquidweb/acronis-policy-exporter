package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var version = "0.0.1"

var (
	listen = kingpin.Flag("listen", "listen address for server").
		Default(":9666").String()
	cacheDir = kingpin.Flag("cachePath", "path to cache directory").
			Default("cache").String()
)

var running sync.WaitGroup

// only thing in main() that doesn't thread and should take some time is NewAPI
func main() {
	kingpin.Version(version)
	kingpin.Parse()
	exiting, shutdown := context.WithCancel(context.Background())

	api, err := NewAPI(exiting, *cid, *secret, *apiTimeout, **acronisURL)
	if err != nil {
		log.Fatal(err)
	}

	policyCfg, err := cacheByPolicy(filepath.Join(*cacheDir, "byPolicy"))
	if err != nil {
		log.Fatalln(err)
	}
	tenantCfg, err := cacheByTenantName(filepath.Join(*cacheDir, "byTenant"))
	if err != nil {
		log.Fatalln(err)
	}

	cachePipeline := multiTaskPipelineFunc(
		filterUpdatesOnly(policyCfg, writeTaskPipeline(policyCfg)),
		filterUpdatesOnly(policyCfg, writeTaskPipeline(tenantCfg)),
	)

	muxer := http.NewServeMux()

	muxer.Handle("/byPolicy", probeHandler(policyCfg.targetToPath))
	muxer.Handle("/byTenant", probeHandler(tenantCfg.targetToPath))
	muxer.Handle("/metrics", promhttp.Handler())
	muxer.Handle("/", rootHandler())

	// create a fn to backfill the cache
	backfill := fillCacheFunc(api, cachePipeline, time.Hour*2, shutdown)
	signalHandler(exiting, shutdown, backfill) // runs after main() exits

	srv := &http.Server{
		Addr:    *listen,
		Handler: muxer,
	}

	err = startServer(exiting, srv) // runs after main() exits
	if err != nil {
		log.Fatalln(err)
	}

	fillCacheFunc(api, cachePipeline, time.Hour*48, shutdown)()
	// set up a regular cache update
	repeatFn(exiting, time.Hour, fillCacheFunc(api, cachePipeline, time.Hour*2, backfill))
	running.Wait() // wait for waitgroup to finish
}

func rootHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
		<html>
		<head><title>Acronis Exporter</title></head>
		<body>
		<h1>Acronis Exporter</h1>
		<p><a href="/byPolicy">Probe</a> - requires a uniq_id GET argument. EX: <a href='/byPolicy?target=FC1E08D9-A52D-4CD6-87A1-76E754D994ED'>/byPolicy?target=FC1E08D9-A52D-4CD6-87A1-76E754D994ED</a>< /p>
		</body>
		</html>`,
		))
	})
}

func signalHandler(quit context.Context, shutdown context.CancelFunc, backfill func()) {
	sigReload := make(chan os.Signal, 1)
	sigQuit := make(chan os.Signal, 1)
	sigPanic := make(chan os.Signal, 1)

	signal.Notify(sigReload, syscall.SIGUSR1, syscall.SIGUSR2)
	signal.Notify(sigQuit, os.Interrupt, syscall.SIGINT, syscall.SIGHUP)
	signal.Notify(sigPanic, syscall.SIGQUIT, syscall.SIGTERM)

	running.Add(1)
	go func() {
		defer running.Done()
		for {
			select {
			case <-quit.Done():
				shutdown()
				return
			case sig := <-sigQuit:
				go func() {
					time.Sleep(time.Second * 20)
					panic(pprof.Lookup("goroutine").WriteTo(os.Stderr, 1))
				}()
				log.Println("got " + sig.String() + " shutting down")
				shutdown()
				return
			case sig := <-sigReload:
				log.Println("got " + sig.String() + " backfilling")
				backfill()
			case sig := <-sigPanic:
				panic("got " + sig.String())
			}
		}
	}()
}

func fillCacheFunc(
	api *AcronisAPI,
	pipeline taskPipelineFunc,
	history time.Duration,
	quit context.CancelFunc,
) func() {
	return func() {
		log.Printf("backfilling cache for %s\n", history.String())
		err := refreshCache(api, pipeline, history)
		if err != nil {
			log.Printf("problem refreshing cache: %v", err)
			quit()
		}
	}
}

func repeatFn(
	dying context.Context,
	interval time.Duration,
	fn func(),
) {
	ticker := time.NewTicker(interval)
	running.Add(1)
	go func() {
		defer running.Done()
		for {
			select {
			case <-dying.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
}

func startServer(quit context.Context, srv *http.Server) error {
	l, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}

	running.Add(1)
	go func() {
		defer running.Done()
		<-quit.Done()
		err := srv.Shutdown(timeoutNoCancel(context.Background(), time.Minute))
		if err != http.ErrServerClosed {
			log.Printf("HTTP shutdown, returned error: %v\n", err)
		}
	}()

	running.Add(1)
	go func() {
		defer running.Done()
		log.Printf("starting http server on %s\n", srv.Addr)
		err := srv.Serve(l)
		if err != http.ErrServerClosed {
			log.Printf("HTTP serve error: %v\n", err)
		}
	}()
	return nil
}
