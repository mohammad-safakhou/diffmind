package main

import (
	"database/sql"
	"net/http"
)

func main() {
	http.HandleFunc("/orders", orderHandler)
}

func orderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_, _ = sql.Open("postgres", "postgres://localhost/db")
	_, _ = http.Post("https://billing.internal/charge", "application/json", nil)
}
