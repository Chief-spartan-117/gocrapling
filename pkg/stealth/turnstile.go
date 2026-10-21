package stealth

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// challengeRectJS locates a Turnstile iframe, including inside shadow roots
// (stealthJS forces them open), scrolls it into view and returns its
// viewport rectangle. Using page JS instead of dom.GetDocument keeps
// chromedp's own node bookkeeping intact.
const challengeRectJS = `((scroll) => {
  const find = (root) => {
    for (const f of root.querySelectorAll('iframe')) {
      if (/challenges\.cloudflare\.com|turnstile/.test(f.src || '')) return f;
    }
    for (const el of root.querySelectorAll('*')) {
      if (el.shadowRoot) { const r = find(el.shadowRoot); if (r) return r; }
    }
    return null;
  };
  const f = find(document);
  if (!f) return null;
  if (scroll) f.scrollIntoView({ block: 'center', behavior: 'instant' });
  const r = f.getBoundingClientRect();
  return { x: r.left, y: r.top, w: r.width, h: r.height };
})`

type rect struct{ X, Y, W, H float64 }

// clickTurnstile moves the mouse to the checkbox of a Turnstile widget and
// clicks it. It reports whether a widget was found.
func clickTurnstile(ctx context.Context) (bool, error) {
	var r *rect
	// Scroll first, then measure again once layout and any smooth
	// scrolling have settled; a stale rectangle clicks empty space.
	if err := chromedp.Evaluate(challengeRectJS+"(true)", &r).Do(ctx); err != nil {
		return false, err
	}
	if r == nil {
		return false, nil
	}
	time.Sleep(400 * time.Millisecond)
	if err := chromedp.Evaluate(challengeRectJS+"(false)", &r).Do(ctx); err != nil {
		return false, err
	}
	if r == nil || r.W == 0 || r.H == 0 {
		return false, nil
	}
	left, top, w, h := r.X, r.Y, r.W, r.H
	// The checkbox sits near the left edge, vertically centred.
	x := left + 28 + rand.Float64()*6
	if w < 100 {
		x = left + w/2
	}
	y := top + h/2 + (rand.Float64()-0.5)*6

	// Approach along a curve from a random nearby start point.
	sx, sy := x-120-rand.Float64()*80, y+40+rand.Float64()*60
	const steps = 14
	for i := 1; i <= steps; i++ {
		t := float64(i) / steps
		e := t * t * (3 - 2*t)
		px, py := sx+(x-sx)*e+rand.Float64()*2, sy+(y-sy)*e+rand.Float64()*2
		if err := input.DispatchMouseEvent(input.MouseMoved, px, py).Do(ctx); err != nil {
			return true, err
		}
		time.Sleep(time.Duration(10+rand.IntN(18)) * time.Millisecond)
	}
	time.Sleep(time.Duration(120+rand.IntN(200)) * time.Millisecond)
	if err := input.DispatchMouseEvent(input.MousePressed, x, y).WithButton(input.Left).WithClickCount(1).Do(ctx); err != nil {
		return true, err
	}
	time.Sleep(time.Duration(40+rand.IntN(70)) * time.Millisecond)
	return true, input.DispatchMouseEvent(input.MouseReleased, x, y).WithButton(input.Left).WithClickCount(1).Do(ctx)
}

// ClickTurnstileAction is a chromedp action for FetchOptions.Actions that
// clicks an embedded Turnstile widget (for example on a login form, where
// SolveChallenge does not apply because the page is not an interstitial).
// It does not fail when no widget is present.
func ClickTurnstileAction() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := clickTurnstile(ctx)
		return err
	})
}
