package main

import (
	"context"
	"database/sql"
	"net/http"

	_ "github.com/lib/pq"
)

func main() {
	db, _ := sql.Open("postgres", "postgres://demo:demo@payments-db:5432/payments")
	_, _ = db.ExecContext(context.Background(), "INSERT INTO payments (status) VALUES ('created')")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/payments", createPayment)
	_ = http.ListenAndServe(":8083", mux)
}

func createPayment(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusCreated)
}
