package main

import (
	"database/sql"
	"net/http"
)

var db *sql.DB

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", getOrder)
	mux.HandleFunc("POST /orders", createOrder)
	http.ListenAndServe(":8080", mux)
}

func getOrder(w http.ResponseWriter, r *http.Request) {
	row := db.QueryRowContext(r.Context(), "SELECT id, total FROM orders WHERE id = $1", r.PathValue("id"))
	_ = row
}

func createOrder(w http.ResponseWriter, r *http.Request) {
	db.ExecContext(r.Context(), "INSERT INTO orders (id, total) VALUES ($1, $2)", r.PathValue("id"), 0)
}
