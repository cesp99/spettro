package main

import (
	"context"
	"fmt"
	"os"

	"spettro/internal/app"
)

func main() {
	a, err := app.New(os.Stdin, os.Stdout, os.Getwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}

	if err := a.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "runtime error: %v\n", err)
		os.Exit(1)
	}
}
