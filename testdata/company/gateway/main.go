package main

import "net/http"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /checkout", checkout)
	http.ListenAndServe(":8080", mux)
}

func checkout(w http.ResponseWriter, r *http.Request) {
	http.Get("http://catalog/products")
	http.Get("http://billing/invoices")
	http.Get("https://status.example.test/health")
}
