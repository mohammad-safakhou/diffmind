package main

import (
	"context"
	"fmt"
	"os"

	"diffmind/internal/app"
)

func main() {
	ctx := context.Background()
	if err := app.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
