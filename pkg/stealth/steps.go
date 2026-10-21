package stealth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// Step is one scripted page action expressed as data, so agents and config
// files can drive the browser without Go code.
//
//	{"type":"click","selector":"button.accept"}
//	{"type":"fill","selector":"#q","text":"golang"}
//	{"type":"press","key":"Enter"}
//	{"type":"wait_for","selector":".results"}
//	{"type":"scroll","pixels":1200}        // or "to":"bottom"
//	{"type":"wait","ms":500}
//	{"type":"evaluate","script":"window.scrollTo(0,0)"}
type Step struct {
	Type     string `json:"type"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Key      string `json:"key,omitempty"`
	Script   string `json:"script,omitempty"`
	MS       int    `json:"ms,omitempty"`
	Pixels   int    `json:"pixels,omitempty"`
	To       string `json:"to,omitempty"`
}

// ParseSteps decodes a JSON array of steps.
func ParseSteps(data []byte) ([]Step, error) {
	var s []Step
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("stealth: bad steps: %w", err)
	}
	return s, nil
}

// Actions converts steps to chromedp actions for FetchOptions.Actions.
func Actions(steps []Step) ([]chromedp.Action, error) {
	out := make([]chromedp.Action, 0, len(steps))
	for i, s := range steps {
		a, err := s.action()
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		out = append(out, a)
	}
	return out, nil
}

var namedKeys = map[string]string{
	"Enter": kb.Enter, "Tab": kb.Tab, "Escape": kb.Escape, "Backspace": kb.Backspace,
	"ArrowDown": kb.ArrowDown, "ArrowUp": kb.ArrowUp, "ArrowLeft": kb.ArrowLeft,
	"ArrowRight": kb.ArrowRight, "PageDown": kb.PageDown, "PageUp": kb.PageUp,
	"Home": kb.Home, "End": kb.End, "Delete": kb.Delete, " ": " ",
}

func (s Step) action() (chromedp.Action, error) {
	need := func(field, v string) error {
		if v == "" {
			return fmt.Errorf("%s step needs %q", s.Type, field)
		}
		return nil
	}
	switch s.Type {
	case "click":
		if err := need("selector", s.Selector); err != nil {
			return nil, err
		}
		return chromedp.Click(s.Selector, chromedp.ByQuery), nil
	case "type":
		if err := need("selector", s.Selector); err != nil {
			return nil, err
		}
		return chromedp.SendKeys(s.Selector, s.Text, chromedp.ByQuery), nil
	case "fill":
		if err := need("selector", s.Selector); err != nil {
			return nil, err
		}
		sel, _ := json.Marshal(s.Selector)
		clear := `(()=>{const e=document.querySelector(` + string(sel) + `);if(e){e.value='';e.dispatchEvent(new Event('input',{bubbles:true}))}})()`
		return chromedp.Tasks{
			chromedp.Focus(s.Selector, chromedp.ByQuery),
			chromedp.Evaluate(clear, nil),
			chromedp.SendKeys(s.Selector, s.Text, chromedp.ByQuery),
		}, nil
	case "press":
		if err := need("key", s.Key); err != nil {
			return nil, err
		}
		k, ok := namedKeys[s.Key]
		if !ok {
			if len([]rune(s.Key)) != 1 {
				return nil, fmt.Errorf("unknown key %q", s.Key)
			}
			k = s.Key
		}
		return chromedp.KeyEvent(k), nil
	case "wait":
		return chromedp.Sleep(time.Duration(s.MS) * time.Millisecond), nil
	case "wait_for":
		if err := need("selector", s.Selector); err != nil {
			return nil, err
		}
		return chromedp.WaitVisible(s.Selector, chromedp.ByQuery), nil
	case "scroll":
		return chromedp.ActionFunc(func(ctx context.Context) error {
			if s.To == "bottom" {
				return chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil).Do(ctx)
			}
			px := s.Pixels
			if px == 0 {
				px = 600
			}
			return input.DispatchMouseEvent(input.MouseWheel, 400, 300).WithDeltaY(float64(px)).Do(ctx)
		}), nil
	case "evaluate":
		if err := need("script", s.Script); err != nil {
			return nil, err
		}
		return chromedp.Evaluate(s.Script, nil), nil
	}
	return nil, fmt.Errorf("unknown step type %q", s.Type)
}
