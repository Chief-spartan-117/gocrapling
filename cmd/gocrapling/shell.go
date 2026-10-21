package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"github.com/sagarshrestha/gocrapling/fetcher"
	"github.com/sagarshrestha/gocrapling/parser"
	"github.com/sagarshrestha/gocrapling/stealth"
)

const shellHelp = `commands:
  get URL                 fetch over HTTP (browser fingerprint)
  post URL BODY           POST a body
  browser URL             fetch in stealth Chrome (runs JavaScript)
  curl <curl command>     run a pasted "Copy as cURL" command
  status | headers        show the last response
  css SELECTOR            select (supports ::text, ::attr(x)); numbers results
  xpath EXPR              select with XPath
  find TEXT               elements whose own text contains TEXT
  similar N               elements similar to result N of the last selection
  path N                  CSS path and XPath of result N
  show N                  outer HTML of result N
  text | md | html        print the page as text, Markdown or HTML (md main = main content only)
  links                   list absolute links
  shot FILE               screenshot to FILE (browser only)
  help | quit`

type shellState struct {
	ctx     context.Context
	out     io.Writer
	f       *fetcher.Fetcher
	browser func() (*stealth.Browser, error)

	resp *fetcher.Response
	doc  *parser.Selector
	sel  parser.Selectors
}

// runShell reads commands from in until EOF or "quit". It is a plain line
// REPL, so it also works when scripted over a pipe.
func runShell(ctx context.Context, in io.Reader, out io.Writer, f *fetcher.Fetcher, browser func() (*stealth.Browser, error)) error {
	st := &shellState{ctx: ctx, out: out, f: f, browser: browser}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	interactive := false
	if fi, err := os.Stdin.Stat(); err == nil && in == os.Stdin && fi.Mode()&os.ModeCharDevice != 0 {
		interactive = true
	}
	prompt := func() {
		if interactive {
			fmt.Fprint(out, "gocrapling> ")
		}
	}
	fmt.Fprintln(out, "gocrapling shell. Type help for commands.")
	prompt()
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			prompt()
			continue
		}
		if line == "quit" || line == "exit" {
			return nil
		}
		if err := st.exec(line); err != nil {
			fmt.Fprintln(out, "error:", err)
		}
		prompt()
	}
	return sc.Err()
}

func (s *shellState) load(resp *fetcher.Response, err error) error {
	if resp == nil {
		return err
	}
	s.resp = resp
	s.sel = nil
	doc, perr := resp.Parse()
	if perr != nil {
		return perr
	}
	s.doc = doc
	fmt.Fprintf(s.out, "HTTP %d  %s  (%d bytes, %s)\n", resp.Status, resp.URL, len(resp.Body), resp.Profile)
	if err != nil {
		fmt.Fprintln(s.out, "warning:", err)
	}
	return nil
}

func (s *shellState) need() error {
	if s.doc == nil {
		return fmt.Errorf("no page yet: use get, browser or curl first")
	}
	return nil
}

func (s *shellState) pick(arg string) (*parser.Selector, error) {
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || n < 1 || n > len(s.sel) {
		return nil, fmt.Errorf("pick a result number between 1 and %d", len(s.sel))
	}
	return s.sel[n-1], nil
}

func (s *shellState) list(sel parser.Selectors) {
	s.sel = sel
	for i, e := range sel {
		if i == 30 {
			fmt.Fprintf(s.out, "... %d more\n", len(sel)-30)
			break
		}
		t := e.Text()
		if len(t) > 90 {
			t = t[:90] + "..."
		}
		fmt.Fprintf(s.out, "%3d  <%s> %s\n", i+1, e.Tag(), t)
	}
	fmt.Fprintf(s.out, "%d results\n", len(sel))
}

func (s *shellState) exec(line string) error {
	cmd, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	switch cmd {
	case "help", "?":
		fmt.Fprintln(s.out, shellHelp)
	case "get":
		return s.load(s.f.Get(ctx, arg))
	case "post":
		u, body, _ := strings.Cut(arg, " ")
		return s.load(s.f.Post(ctx, u, "application/x-www-form-urlencoded", []byte(body)))
	case "browser":
		b, err := s.browser()
		if err != nil {
			return err
		}
		return s.load(b.Fetch(ctx, arg, stealth.FetchOptions{}))
	case "curl":
		req, err := fetcher.ParseCurl("curl " + arg)
		if err != nil {
			return err
		}
		fmt.Fprintf(s.out, "%s %s\n", req.Method, req.URL)
		return s.load(s.f.Do(ctx, req))
	case "status":
		if s.resp == nil {
			return fmt.Errorf("no page yet")
		}
		fmt.Fprintf(s.out, "%d %s proto=%s blocked=%v attempts=%d\n", s.resp.Status, s.resp.URL, s.resp.Proto, s.resp.Blocked, s.resp.Attempts)
	case "headers":
		if s.resp == nil {
			return fmt.Errorf("no page yet")
		}
		for k, v := range s.resp.Headers {
			if k == http.HeaderOrderKey || k == http.PHeaderOrderKey {
				continue
			}
			fmt.Fprintf(s.out, "%s: %s\n", k, strings.Join(v, ", "))
		}
	case "css":
		if err := s.need(); err != nil {
			return err
		}
		if strings.Contains(arg, "::") {
			for _, v := range s.doc.Extract(arg) {
				fmt.Fprintln(s.out, v)
			}
			return nil
		}
		sel, err := s.doc.Select(arg)
		if err != nil {
			return err
		}
		s.list(sel)
	case "xpath":
		if err := s.need(); err != nil {
			return err
		}
		sel, err := s.doc.XPath(arg)
		if err != nil {
			return err
		}
		s.list(sel)
	case "find":
		if err := s.need(); err != nil {
			return err
		}
		s.list(s.doc.FindByText(arg, true, ""))
	case "similar":
		e, err := s.pick(arg)
		if err != nil {
			return err
		}
		s.list(e.FindSimilar(0.7))
	case "path":
		e, err := s.pick(arg)
		if err != nil {
			return err
		}
		fmt.Fprintf(s.out, "css:   %s\nxpath: %s\n", e.CSSPath(), e.XPathExpr())
	case "show":
		e, err := s.pick(arg)
		if err != nil {
			return err
		}
		fmt.Fprintln(s.out, e.HTML())
	case "text":
		if err := s.need(); err != nil {
			return err
		}
		fmt.Fprintln(s.out, s.doc.Text())
	case "md":
		if err := s.need(); err != nil {
			return err
		}
		if arg == "main" {
			fmt.Fprint(s.out, s.doc.MarkdownMain())
		} else {
			fmt.Fprint(s.out, s.doc.Markdown())
		}
	case "html":
		if s.resp == nil {
			return fmt.Errorf("no page yet")
		}
		fmt.Fprintln(s.out, s.resp.Text())
	case "links":
		if err := s.need(); err != nil {
			return err
		}
		re := regexp.MustCompile(`^https?://`)
		for _, h := range s.doc.Extract("a::attr(href)") {
			if re.MatchString(h) {
				fmt.Fprintln(s.out, h)
			}
		}
	case "shot":
		b, err := s.browser()
		if err != nil {
			return err
		}
		if s.resp == nil {
			return fmt.Errorf("no page yet")
		}
		r, err := b.Fetch(ctx, s.resp.URL, stealth.FetchOptions{Screenshot: true})
		if err != nil {
			return err
		}
		if err := os.WriteFile(arg, r.Screenshot, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "wrote %s (%d bytes)\n", arg, len(r.Screenshot))
	default:
		return fmt.Errorf("unknown command %q (try help)", cmd)
	}
	return nil
}
