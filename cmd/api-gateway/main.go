package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"ticketflow/internal/config"
	"ticketflow/internal/httpx"
)

//go:embed web/*
var webAssets embed.FS

func main() {
	routes := []route{
		{prefix: "/users", target: config.Env("USER_SERVICE_URL", "http://localhost:8081")},
		{prefix: "/events", target: config.Env("EVENT_SERVICE_URL", "http://localhost:8082")},
		{prefix: "/orders", target: config.Env("ORDER_SERVICE_URL", "http://localhost:8083")},
		{prefix: "/notifications", target: config.Env("NOTIFICATION_SERVICE_URL", "http://localhost:8084")},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	for _, rt := range routes {
		proxy := mustProxy(rt.target)
		prefix := rt.prefix
		mux.HandleFunc(prefix, proxyHandler(prefix, proxy))
		mux.HandleFunc(prefix+"/", proxyHandler(prefix, proxy))
	}
	mux.Handle("/", webHandler())

	addr := ":" + config.Env("PORT", "8080")
	log.Printf("api-gateway listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type route struct {
	prefix string
	target string
}

func mustProxy(rawURL string) *httputil.ReverseProxy {
	target, err := url.Parse(rawURL)
	if err != nil {
		log.Fatalf("bad upstream url %s: %v", rawURL, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
		r.Header.Set("X-Forwarded-Host", r.Host)
		r.Header.Set("X-Gateway", "ticketflow")
	}
	return proxy
}

func proxyHandler(prefix string, proxy *httputil.ReverseProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			httpx.Error(w, http.StatusNotFound, "not found")
			return
		}
		proxy.ServeHTTP(w, r)
	}
}

func webHandler() http.Handler {
	webRoot, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("web assets unavailable: %v", err)
	}
	return http.FileServer(http.FS(webRoot))
}
