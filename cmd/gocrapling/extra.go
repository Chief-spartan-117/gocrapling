package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagarshrestha/gocrapling/browserinstall"
	"github.com/sagarshrestha/gocrapling/fetcher"
	"github.com/sagarshrestha/gocrapling/parser"
	"github.com/sagarshrestha/gocrapling/stealth"
)

// pageOpts are the per-page browser options shared by fetch and extract.
type pageOpts struct {
	waitSelector string
	stepsFile    string
}

func (p *pageOpts) register(fs *flag.FlagSet) {
	fs.StringVar(&p.waitSelector, "wait-selector", "", "browser: wait until this CSS selector exists")
	fs.StringVar(&p.stepsFile, "steps", "", "browser: JSON file of page actions (click, type, fill, press, wait, wait_for, scroll, evaluate)")
}

// get downloads target with plain HTTP, or with Chrome when -browser or any
// browser-only option is set. The returned func releases the client.
func (c *common) get(ctx context.Context, target string, p pageOpts) (*fetcher.Response, func(), error) {
	if !c.stealthy && p.waitSelector == "" && p.stepsFile == "" {
		f := c.fetcher()
		resp, err := f.Do(ctx, fetcher.Request{URL: target})
		return resp, f.Close, err
	}
	b, err := c.browser()
	if err != nil {
		return nil, func() {}, err
	}
	fo := stealth.FetchOptions{WaitSelector: p.waitSelector}
	if p.stepsFile != "" {
		data, err := os.ReadFile(p.stepsFile)
		if err != nil {
			b.Close()
			return nil, func() {}, err
		}
		steps, err := stealth.ParseSteps(data)
		if err != nil {
			b.Close()
			return nil, func() {}, err
		}
		if fo.Actions, err = stealth.Actions(steps); err != nil {
			b.Close()
			return nil, func() {}, err
		}
	}
	resp, err := b.Fetch(ctx, target, fo)
	return resp, b.Close, err
}

func cmdInstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	force := fs.Bool("force", false, "re-download even if already installed")
	_ = fs.Parse(args)
	path, err := browserinstall.Install(ctx, *force, os.Stderr)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// cmdExtract downloads a page and writes it to a file; the extension picks
// the format: .md (Markdown), .txt (text) or .html (raw HTML).
func cmdExtract(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	var c common
	c.register(fs)
	var p pageOpts
	p.register(fs)
	css := fs.String("css", "", "only this part of the page (CSS selector)")
	ai := fs.Bool("ai-targeted", false, "strip hidden content and invisible characters (prompt-injection defence)")
	mainOnly := fs.Bool("main", false, "main content only (.md)")
	_ = fs.Parse(reorderArgs(args))
	if fs.NArg() != 2 {
		return errors.New("usage: gocrapling extract [flags] URL FILE")
	}
	target, out := fs.Arg(0), fs.Arg(1)
	ext := strings.ToLower(filepath.Ext(out))
	if ext != ".md" && ext != ".txt" && ext != ".html" && ext != ".htm" {
		return fmt.Errorf("unsupported output extension %q (use .md, .txt or .html)", ext)
	}

	resp, closeFn, err := c.get(ctx, target, p)
	defer closeFn()
	if resp == nil {
		return err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	var body string
	if ext == ".html" || ext == ".htm" {
		body = resp.Text()
		if *css != "" {
			doc, err := resp.Parse()
			if err != nil {
				return err
			}
			var parts []string
			for _, n := range doc.CSS(*css) {
				parts = append(parts, n.HTML())
			}
			body = strings.Join(parts, "\n")
		}
	} else {
		doc, err := resp.Parse()
		if err != nil {
			return err
		}
		scope := doc
		if *css != "" {
			sel := doc.CSS(*css)
			if len(sel) == 0 {
				return fmt.Errorf("selector %q matched nothing", *css)
			}
			scope = sel.First()
		}
		if ext == ".md" {
			body = scope.MarkdownWith(parser.MarkdownOptions{MainOnly: *mainOnly, Safe: *ai})
		} else if *ai {
			body = scope.SafeText()
		} else {
			body = scope.Text()
		}
	}
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes, HTTP %d)\n", out, len(body), resp.Status)
	return nil
}

// reorderArgs moves flags before positionals so "extract URL FILE -css x"
// works; Go's flag package stops at the first non-flag.
func reorderArgs(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			// a value-taking flag without "=" consumes the next arg
			if !strings.Contains(a, "=") && !isBoolFlag(a) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

var boolFlags = map[string]bool{
	"headed": true, "browser": true, "block-ads": true, "http3": true, "google-referer": true,
	"ai-targeted": true, "main": true, "disable-resources": true, "network-idle": true, "force": true,
}

func isBoolFlag(a string) bool { return boolFlags[strings.TrimLeft(a, "-")] }
