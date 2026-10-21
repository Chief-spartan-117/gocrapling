// Tries to pass an embedded Cloudflare Turnstile widget and reports whether
// the page received a response token. Usage: go run ./examples/turnstile URL
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: turnstile URL")
		os.Exit(2)
	}
	b, err := stealth.New(stealth.Options{Headed: os.Getenv("HEADED") != "", Timeout: 90 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer b.Close()

	var token string
	resp, err := b.Fetch(context.Background(), os.Args[1], stealth.FetchOptions{
		Humanize:   true,
		Screenshot: true,
		Settle:     3 * time.Second,
		Actions: []chromedp.Action{
			stealth.ClickTurnstileAction(),
			chromedp.Sleep(6 * time.Second),
			chromedp.Evaluate(`(document.querySelector('input[name="cf-turnstile-response"]')||{}).value||''`, &token),
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if out := os.Getenv("SHOT"); out != "" {
		_ = os.WriteFile(out, resp.Screenshot, 0o644)
	}
	fmt.Printf("token length: %d\n", len(token))
}
