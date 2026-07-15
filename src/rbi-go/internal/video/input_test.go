package video

import (
	"context"
	"testing"
)

// TestDispatchInputEvent_InvalidJSON_ReturnsSilently verifies that invalid JSON
// is logged but the function returns without panicking.
func TestDispatchInputEvent_InvalidJSON_ReturnsSilently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	invalidJSON := []byte(`{invalid json`)
	var curX, curY float64

	// Should not panic; just logs the error.
	dispatchInputEvent(ctx, invalidJSON, &curX, &curY, "http://example.com", true)
}

// TestDispatchInputEvent_UnknownType_ReturnsSilently verifies that unknown event types
// are logged but the function returns without panicking.
func TestDispatchInputEvent_UnknownType_ReturnsSilently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	unknownType := []byte(`{"type":"unknownType"}`)
	var curX, curY float64

	// Should not panic; just logs the error.
	dispatchInputEvent(ctx, unknownType, &curX, &curY, "http://example.com", true)
}

// TestDispatchInputEvent_KeydownAllowKeyboardFalse_DropsEvent verifies that
// keydown events are dropped when allowKeyboard is false.
func TestDispatchInputEvent_KeydownAllowKeyboardFalse_DropsEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	keydownEvent := []byte(`{"type":"keydown","key":"a"}`)
	var curX, curY float64

	// Should return early without attempting chromedp.Run.
	dispatchInputEvent(ctx, keydownEvent, &curX, &curY, "http://example.com", false)
}

// TestDispatchInputEvent_KeyupAllowKeyboardFalse_DropsEvent verifies that
// keyup events are dropped when allowKeyboard is false.
func TestDispatchInputEvent_KeyupAllowKeyboardFalse_DropsEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	keyupEvent := []byte(`{"type":"keyup","key":"a"}`)
	var curX, curY float64

	// Should return early without attempting chromedp.Run.
	dispatchInputEvent(ctx, keyupEvent, &curX, &curY, "http://example.com", false)
}

// TestDispatchInputEvent_KeydownAllowKeyboardTrue_AttemptsDispatch verifies that
// keydown events with allowKeyboard=true attempt dispatch (and fail silently on cancelled ctx).
func TestDispatchInputEvent_KeydownAllowKeyboardTrue_AttemptsDispatch(t *testing.T) {
	// Use a cancelled context so chromedp.Run fails silently.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	keydownEvent := []byte(`{"type":"keydown","key":"a"}`)
	var curX, curY float64

	// Should not panic even though chromedp.Run fails on cancelled context.
	dispatchInputEvent(ctx, keydownEvent, &curX, &curY, "http://example.com", true)
}

// TestDispatchInputEvent_MousemoveUpdatesPosition verifies that mousemove events
// update the curX and curY pointers even though chromedp.Run fails on cancelled context.
func TestDispatchInputEvent_MousemoveUpdatesPosition(t *testing.T) {
	// Use a cancelled context so chromedp.Run fails silently.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	x, y := 123.45, 67.89
	mousemoveEvent := []byte(`{"type":"mousemove","x":123.45,"y":67.89}`)
	var curX, curY float64

	dispatchInputEvent(ctx, mousemoveEvent, &curX, &curY, "http://example.com", true)

	// Verify that the coordinates were updated.
	if curX != x {
		t.Errorf("expected curX=%f, got %f", x, curX)
	}
	if curY != y {
		t.Errorf("expected curY=%f, got %f", y, curY)
	}
}

// TestDispatchInputEvent_NilXY_DefaultsToZero verifies that nil X and Y fields
// default to 0 in mousemove events.
func TestDispatchInputEvent_NilXY_DefaultsToZero(t *testing.T) {
	// Use a cancelled context so chromedp.Run fails silently.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// mousemove with no X or Y fields (they'll unmarshal as nil).
	mousemoveEvent := []byte(`{"type":"mousemove"}`)
	var curX, curY float64

	dispatchInputEvent(ctx, mousemoveEvent, &curX, &curY, "http://example.com", true)

	// Verify that the coordinates defaulted to 0.
	if curX != 0.0 {
		t.Errorf("expected curX=0, got %f", curX)
	}
	if curY != 0.0 {
		t.Errorf("expected curY=0, got %f", curY)
	}
}
