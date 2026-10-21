// Prints the TLS/HTTP2 fingerprint a remote server sees for this client.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
)

func main() {
	f := fetcher.New(fetcher.Options{Profile: "chrome", Timeout: 20 * time.Second})
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := f.Get(ctx, "https://tls.peet.ws/api/clean")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(r.Status, r.Profile, r.Proxy)
	fmt.Println(r.Text())
}
