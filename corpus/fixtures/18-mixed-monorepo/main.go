package main

func main() {
	r.GET("/ready", nil)
}

type Router struct{}
var r = &Router{}
func (rr *Router) GET(_ string, _ any) {}
