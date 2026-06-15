package main

import "net/http"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /version", version)
	http.ListenAndServe(":8080", mux)
}

func health(w http.ResponseWriter, r *http.Request)  {}
func version(w http.ResponseWriter, r *http.Request) {}
