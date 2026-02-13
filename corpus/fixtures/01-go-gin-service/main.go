package main

func main() {
	r := NewRouter()
	r.GET("/health", nil)
}

type Router struct{}
func NewRouter() *Router { return &Router{} }
func (r *Router) GET(_ string, _ any) {}
