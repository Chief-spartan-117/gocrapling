package service

import (
	"context"
	"errors"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

// Screenshot renders url in stealth Chrome and returns a full-page PNG.
func (s *Service) Screenshot(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("url is required")
	}
	b, err := s.getBrowser()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := b.Fetch(ctx, url, stealth.FetchOptions{Screenshot: true})
	if err != nil {
		return nil, err
	}
	return resp.Screenshot, nil
}
