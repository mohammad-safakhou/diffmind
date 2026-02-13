package main

import "net/http"

func main() {
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	_ = req
}
