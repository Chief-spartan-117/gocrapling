package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

// CaptureArgs are the inputs of capture_requests and POST /v1/capture.
type CaptureArgs struct {
	URL      string `json:"url"`
	Regex    string `json:"url_regex"`
	MaxChars int    `json:"max_chars"`
}

// Captured is one background request a page made.
type Captured struct {
	URL      string `json:"url"`
	Method   string `json:"method"`
	Status   int    `json:"status"`
	MimeType string `json:"mime_type"`
	Body     string `json:"body"`
}

// Capture loads a page in Chrome and returns the XHR/fetch responses it made.
func (s *Service) Capture(ctx context.Context, a CaptureArgs) ([]Captured, error) {
	if a.URL == "" {
		return nil, errors.New("url is required")
	}
	var re *regexp.Regexp
	if a.Regex != "" {
		var err error
		if re, err = regexp.Compile(a.Regex); err != nil {
			return nil, fmt.Errorf("bad url_regex: %w", err)
		}
	}
	b, err := s.getBrowser()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := b.Fetch(ctx, a.URL, stealth.FetchOptions{Record: true, CaptureFilter: re})
	if err != nil {
		return nil, err
	}
	out := make([]Captured, 0, len(resp.Captured))
	for _, c := range resp.Captured {
		out = append(out, Captured{URL: c.URL, Method: c.Method, Status: c.Status, MimeType: c.MimeType, Body: string(c.Body)})
	}
	return out, nil
}
