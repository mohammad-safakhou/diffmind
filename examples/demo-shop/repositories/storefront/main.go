package main

import (
	"bytes"
	"net/http"
)

func submitCheckout() (*http.Response, error) {
	body := bytes.NewBufferString(`{"customerId":"demo-user","postalCode":"10115","items":["sku-1"]}`)
	return http.Post("http://gateway/api/checkout", "application/json", body)
}

func main() {
	_, _ = submitCheckout()
}
