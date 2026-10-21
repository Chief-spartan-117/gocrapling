package fetcher

import (
	"bytes"
	"errors"
)

// ErrBlocked is returned when every attempt hit an anti-bot wall.
var ErrBlocked = errors.New("fetcher: blocked by anti-bot protection")

var blockMarkers = [][]byte{
	[]byte("cf-chl-"),
	[]byte("Just a moment..."),
	[]byte("challenge-platform"),
	[]byte("Attention Required! | Cloudflare"),
	[]byte("_Incapsula_Resource"),
	[]byte("px-captcha"),
	[]byte("captcha-delivery.com"), // DataDome
	[]byte("Access Denied</title>"),
	[]byte("Pardon Our Interruption"),
	[]byte("g-recaptcha"),
	[]byte("h-captcha"),
}

// IsBlocked reports whether a response looks like an anti-bot block rather
// than real content. Status codes 403/429/503 are suspect only; large
// bodies with real content are not treated as blocks by marker alone.
func IsBlocked(status int, body []byte) bool {
	switch status {
	case 401, 403, 429, 503:
		return true
	}
	// Challenge pages are small. A marker inside a big page is usually a
	// legitimate embedded captcha widget.
	if len(body) > 60_000 {
		return false
	}
	for _, m := range blockMarkers {
		if bytes.Contains(body, m) {
			return true
		}
	}
	return false
}
