package main

import (
	"net/http"

	"gorm.io/gorm"
)

type Order struct {
	ID    uint
	Total int64
}

var db *gorm.DB

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", getOrder)
	mux.HandleFunc("POST /orders", createOrder)
	http.ListenAndServe(":8080", mux)
}

func getOrder(w http.ResponseWriter, r *http.Request) {
	var order Order
	db.First(&order, r.PathValue("id"))
	_ = order
}

func createOrder(w http.ResponseWriter, r *http.Request) {
	db.Create(&Order{Total: 100})
}
