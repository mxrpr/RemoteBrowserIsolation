package video

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	pion "github.com/pion/webrtc/v3"
)

// inputEvent mirrors the JSON object sent by index.html over the data channel.
// Field names match the camelCase keys produced by the browser-side send()
// function. The C# equivalent is the InputEvent record.
//
// Supported event types (field usage per type):
//
//	"mousemove" — X, Y
//	"mousedown" — X, Y
//	"mouseup"   — X, Y
//	"wheel"     — DeltaX, DeltaY
//	"keydown"   — Key
//	"keyup"     — Key
type inputEvent struct {
	Type   string   `json:"type"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	DeltaX *float64 `json:"deltaX,omitempty"`
	DeltaY *float64 `json:"deltaY,omitempty"`
	Key    *string  `json:"key,omitempty"`
}

// WireInputForwarder registers the OnMessage and OnClose handlers on the pion
// data channel and starts two goroutines:
//
//  1. A queue goroutine that reads from a small buffered inCh (fed by the pion
//     OnMessage callback) and accumulates events in an internal [][]byte slice,
//     then drains them one-at-a-time into an unbuffered outCh. The slice grows
//     without bound, so no event is ever dropped regardless of how fast they
//     arrive — a mouseup that follows a mousedown is always delivered in order.
//
//  2. replayLoop, which reads from outCh and dispatches each event as a CDP
//     command. CDP calls can be slow (round-trips into Chrome), so decoupling
//     the pion callback from CDP dispatch via the queue goroutine keeps pion
//     non-blocking without sacrificing event ordering.
//
// Mouse events (mousemove, mousedown, mouseup, wheel) are always replayed.
// Keyboard events (keydown, keyup) are replayed only if allowKeyboard is true;
// when false, keystrokes are dropped server-side — this is the server-
// authoritative enforcement for VideoNoInput mode, independent of whether the
// client sends them or not.
//
// sessCtx is the browser session's chromedp context. CDP input dispatch
// commands run against this context. Both goroutines exit when sessCtx is
// cancelled or the data channel is closed.
func WireInputForwarder(ch *pion.DataChannel, sessCtx context.Context, targetURL string, allowKeyboard bool) {
	// inCh buffers events from pion's OnMessage callback. The buffer is large
	// enough that the callback is practically never blocked; the queue goroutine
	// drains inCh almost immediately (no CDP I/O happens here).
	inCh := make(chan []byte, 64)
	// outCh is the unbuffered handoff from the queue goroutine to replayLoop.
	outCh := make(chan []byte)

	ch.OnMessage(func(msg pion.DataChannelMessage) {
		select {
		case inCh <- msg.Data:
		case <-sessCtx.Done():
			// Session is ending; ignore further events.
		}
	})

	ch.OnClose(func() {
		close(inCh)
	})

	// Queue goroutine: moves items from inCh into an unbounded internal slice
	// and drains them into outCh in arrival order.
	go func() {
		defer close(outCh)
		var buf [][]byte
		for {
			if len(buf) == 0 {
				// Nothing buffered — block until a new event arrives or inCh closes.
				item, ok := <-inCh
				if !ok {
					return // data channel closed, nothing left to drain
				}
				buf = append(buf, item)
			} else {
				// Have buffered items: race between accepting a new event and
				// handing the oldest buffered event to replayLoop.
				select {
				case item, ok := <-inCh:
					if !ok {
						// inCh closed; drain the remaining buffered events before exiting.
						for _, item := range buf {
							select {
							case outCh <- item:
							case <-sessCtx.Done():
								return
							}
						}
						return
					}
					buf = append(buf, item)
				case outCh <- buf[0]:
					buf = buf[1:]
				case <-sessCtx.Done():
					return
				}
			}
		}
	}()

	go replayLoop(sessCtx, outCh, targetURL, allowKeyboard)
}

// replayLoop is the single input consumer: reads queued events in arrival order
// and replays each one as a CDP Input.dispatch* command against sessCtx.
// Exits when the queue is closed (data channel closed) or sessCtx is cancelled.
func replayLoop(sessCtx context.Context, queue <-chan []byte, targetURL string, allowKeyboard bool) {
	// Track the virtual mouse position so wheel events (which carry deltas, not
	// absolute coordinates) can be dispatched at the correct page location.
	var curX, curY float64

	for {
		var data []byte
		select {
		case d, ok := <-queue:
			if !ok {
				return // data channel closed
			}
			data = d
		case <-sessCtx.Done():
			return
		}

		dispatchInputEvent(sessCtx, data, &curX, &curY, targetURL, allowKeyboard)
	}
}

// dispatchInputEvent decodes one raw JSON message from the data channel and
// dispatches the corresponding CDP Input command against sessCtx.
// curX/curY track the virtual mouse position across calls.
func dispatchInputEvent(
	sessCtx context.Context,
	data []byte,
	curX, curY *float64,
	targetURL string,
	allowKeyboard bool,
) {
	t0 := time.Now()

	var ev inputEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		slog.Warn("video: unmarshal input event", "url", targetURL, "err", err)
		return
	}

	var action chromedp.Action
	switch ev.Type {
	case "mousemove":
		x, y := ptrFloat(ev.X), ptrFloat(ev.Y)
		*curX, *curY = x, y
		action = input.DispatchMouseEvent(input.MouseMoved, x, y)

	case "mousedown":
		x, y := ptrFloat(ev.X), ptrFloat(ev.Y)
		*curX, *curY = x, y
		// Move to coordinates first (matching Playwright's DownAsync which acts
		// at the virtual mouse's current position) then press.
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			if err := input.DispatchMouseEvent(input.MouseMoved, x, y).Do(ctx); err != nil {
				return err
			}
			return input.DispatchMouseEvent(input.MousePressed, x, y).
				WithButton(input.Left).
				WithClickCount(1).
				Do(ctx)
		})

	case "mouseup":
		x, y := ptrFloat(ev.X), ptrFloat(ev.Y)
		*curX, *curY = x, y
		action = chromedp.ActionFunc(func(ctx context.Context) error {
			if err := input.DispatchMouseEvent(input.MouseMoved, x, y).Do(ctx); err != nil {
				return err
			}
			return input.DispatchMouseEvent(input.MouseReleased, x, y).
				WithButton(input.Left).
				Do(ctx)
		})

	case "wheel":
		dx, dy := ptrFloat(ev.DeltaX), ptrFloat(ev.DeltaY)
		// Wheel events carry deltas, not absolute coordinates; dispatch at the
		// current tracked mouse position (matches Playwright's WheelAsync).
		cx, cy := *curX, *curY
		action = input.DispatchMouseEvent(input.MouseWheel, cx, cy).
			WithDeltaX(dx).
			WithDeltaY(dy)

	case "keydown":
		if !allowKeyboard {
			// VideoNoInput: drop keyboard events server-side.
			return
		}
		key := ptrString(ev.Key)
		action = input.DispatchKeyEvent(input.KeyDown).WithKey(key)

	case "keyup":
		if !allowKeyboard {
			return
		}
		key := ptrString(ev.Key)
		action = input.DispatchKeyEvent(input.KeyUp).WithKey(key)

	default:
		slog.Warn("video: unknown input event type", "type", ev.Type, "url", targetURL)
		return
	}

	if err := chromedp.Run(sessCtx, action); err != nil {
		// sessCtx cancellation during teardown produces errors here — expected.
		if sessCtx.Err() == nil {
			slog.Warn("video: dispatch input event", "type", ev.Type, "url", targetURL, "err", err)
		}
		return
	}

	slog.Debug("video: input event dispatched",
		"type", ev.Type,
		"url", targetURL,
		"ms", time.Since(t0).Milliseconds(),
	)
}

// ptrFloat returns the value of a *float64, defaulting to 0 if nil.
func ptrFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ptrString returns the value of a *string, defaulting to "" if nil.
func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
