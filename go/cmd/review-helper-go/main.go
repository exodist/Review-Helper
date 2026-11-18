// Command review-helper-go is the Go implementation of the Review Helper
// standard (standards/CLI.md), run through the review-helper dispatcher.
package main

import (
	"os"

	"github.com/exodist/Review-Helper/go/internal/review"
)

func main() {
	os.Exit(review.Run(os.Args[1:], os.Stdout, os.Stderr))
}
