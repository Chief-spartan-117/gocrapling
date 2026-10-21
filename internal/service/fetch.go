package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
	"github.com/Chief-spartan-117/gocrapling/pkg/parser"
	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

// FetchArgs are the inputs of the fetch tool and of POST /v1/fetch.
type FetchArgs struct {
	URL      string `json:"url"`
	Mode     string `json:"mode"`
	CSS      string `json:"css"`
	XPath    string `json:"xpath"`
	MainOnly bool   `json:"main_only"`
	RawHTML  bool   `json:"raw_html"`
	MaxChars int    `json:"max_chars"`

	AITargeted   bool            `json:"ai_targeted"`
	WaitSelector string          `json:"wait_selector"`
	NetworkIdle  bool            `json:"network_idle"`
	Actions      json.RawMessage `json:"actions"`
	StateFile    string          `json:"state_file"`
}

// FetchResult is what a fetch produced.
type FetchResult struct {
	Status  int    `json:"status"`
	URL     string `json:"url"`
	Blocked bool   `json:"blocked"`
	// Warning carries a non-fatal error, e.g. a block page that survived retries.
	Warning string `json:"warning,omitempty"`
	// Content is Markdown, raw HTML, or the matches joined by newlines.
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
	// Results lists each match when css or xpath was given.
	Results []string `json:"results,omitempty"`
}

// Fetch downloads and extracts one page.
func (s *Service) Fetch(ctx context.Context, a FetchArgs) (*FetchResult, error) {
	if a.URL == "" {
		return nil, errors.New("url is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	browserOnly := a.WaitSelector != "" || a.NetworkIdle || len(a.Actions) > 0 || a.StateFile != ""
	var resp *fetcher.Response
	var err error
	if a.Mode == "browser" || browserOnly {
		b, berr := s.getBrowser()
		if berr != nil {
			return nil, berr
		}
		fo := stealth.FetchOptions{WaitSelector: a.WaitSelector, WaitIdle: a.NetworkIdle}
		if len(a.Actions) > 0 {
			steps, serr := stealth.ParseSteps(a.Actions)
			if serr != nil {
				return nil, fmt.Errorf("actions: %w", serr)
			}
			if fo.Actions, serr = stealth.Actions(steps); serr != nil {
				return nil, fmt.Errorf("actions: %w", serr)
			}
		}
		statePath := ""
		if a.StateFile != "" {
			if statePath, err = s.statePath(a.StateFile); err != nil {
				return nil, err
			}
			if lerr := b.LoadState(ctx, statePath); lerr != nil {
				s.log.Warn("load state", "err", lerr)
			}
		}
		resp, err = b.Fetch(ctx, a.URL, fo)
		if statePath != "" && resp != nil {
			if serr := b.SaveState(ctx, statePath); serr != nil {
				s.log.Warn("save state", "err", serr)
			}
		}
	} else {
		resp, err = s.f.Get(ctx, a.URL)
	}
	if resp == nil {
		return nil, err
	}
	out := &FetchResult{Status: resp.Status, URL: resp.URL, Blocked: resp.Blocked}
	if err != nil {
		out.Warning = err.Error()
	}

	var body string
	switch {
	case a.RawHTML:
		body = resp.Text()
	default:
		doc, perr := resp.Parse()
		if perr != nil {
			return nil, perr
		}
		switch {
		case a.CSS != "":
			out.Results = doc.Extract(a.CSS)
		case a.XPath != "":
			sel, xerr := doc.XPath(a.XPath)
			if xerr != nil {
				return nil, xerr
			}
			out.Results = sel.Texts()
		default:
			body = doc.MarkdownWith(parser.MarkdownOptions{MainOnly: a.MainOnly, Safe: a.AITargeted})
		}
		if out.Results != nil {
			if a.AITargeted {
				for i := range out.Results {
					out.Results[i] = parser.CleanInvisible(out.Results[i])
				}
			}
			body = strings.Join(out.Results, "\n")
		}
	}
	out.Content, out.Truncated = s.cut(body, a.MaxChars)
	return out, nil
}
