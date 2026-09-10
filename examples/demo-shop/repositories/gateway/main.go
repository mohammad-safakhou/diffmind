package main

import (
	"bytes"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/checkout", checkout)
	_ = http.ListenAndServe(":8080", mux)
}

func checkout(w http.ResponseWriter, _ *http.Request) {
	_, _ = http.Get("http://catalog/products")
	body := bytes.NewBufferString(`{"customerId":"demo-user","postalCode":"10115","items":["sku-1"]}`)
	_, _ = http.Post("http://checkout/v1/checkout", "application/json", body)
	_, _ = http.Get("https://status.example.test/health")
	w.WriteHeader(http.StatusAccepted)
}
