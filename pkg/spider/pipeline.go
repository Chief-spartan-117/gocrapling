package spider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"
)

// Pipeline receives every scraped item.
type Pipeline interface {
	Process(ctx context.Context, item any) error
	Close() error
}

// JSONLPipeline appends one JSON object per line. Safe for concurrent use.
type JSONLPipeline struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

// NewJSONLPipeline opens (appending) the file at path.
func NewJSONLPipeline(path string) (*JSONLPipeline, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONLPipeline{f: f, w: bufio.NewWriter(f)}, nil
}

func (p *JSONLPipeline) Process(_ context.Context, item any) error {
	b, err := json.Marshal(item)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return p.w.Flush() // flush per item so a crash loses nothing
}

func (p *JSONLPipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.w.Flush(); err != nil {
		p.f.Close()
		return err
	}
	return p.f.Close()
}

// FuncPipeline adapts a function.
type FuncPipeline func(ctx context.Context, item any) error

func (f FuncPipeline) Process(ctx context.Context, item any) error { return f(ctx, item) }
func (f FuncPipeline) Close() error                                { return nil }

// JSONPipeline writes all items as one JSON array, streaming as it goes.
// Close terminates the array; an interrupted run without Close leaves an
// unterminated file (use JSONLPipeline when that matters).
type JSONPipeline struct {
	mu    sync.Mutex
	f     *os.File
	w     *bufio.Writer
	count int
}

// NewJSONPipeline creates (truncates) path.
func NewJSONPipeline(path string) (*JSONPipeline, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	p := &JSONPipeline{f: f, w: bufio.NewWriter(f)}
	if _, err := p.w.WriteString("[\n"); err != nil {
		f.Close()
		return nil, err
	}
	return p, nil
}

func (p *JSONPipeline) Process(_ context.Context, item any) error {
	b, err := json.MarshalIndent(item, "  ", "  ")
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count > 0 {
		p.w.WriteString(",\n")
	}
	p.count++
	p.w.WriteString("  ")
	p.w.Write(b)
	return p.w.Flush()
}

func (p *JSONPipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.w.WriteString("\n]\n")
	if err := p.w.Flush(); err != nil {
		p.f.Close()
		return err
	}
	return p.f.Close()
}
