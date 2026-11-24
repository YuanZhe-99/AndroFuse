package gui

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/scrcpy"
)

// InputActionKind classifies a translated desktop input action for a session window.
type InputActionKind uint8

const (
	ActionNone InputActionKind = iota
	ActionTouch
	ActionScroll
	ActionKey
	ActionText
	ActionReleaseFocus
	ActionToggleFullscreen
)

// TranslatedInput describes one protocol-agnostic input action derived from ui.InputEvent.
type TranslatedInput struct {
	Kind         InputActionKind
	TouchAction  byte
	Button       uint32
	ButtonsHeld  uint32
	VideoX       int32
	VideoY       int32
	HScroll      float64
	VScroll      float64
	Keycode      uint32
	KeyAction    byte
	MetaState    uint32
	Text         string
	ReleaseFocus bool
}

// InputTracker translates MyGo pointer, scroll, key, and IME text events into
// Android scrcpy coordinates/keycodes while avoiding double-typing on Space and
// supporting emergency focus release (`Ctrl+Alt+Esc`, INV-12).
type InputTracker struct {
	buttonsHeld   uint32
	heldKeys      map[ui.Key]uint32
	suppressSpace bool
}

// FitBox computes the top-left offset and width/height in DIPs where a videoW x videoH
// frame renders inside box under `ui.Contain`.
func FitBox(box ui.Rect, videoW, videoH int) (ox, oy, w, h float32) {
	if videoW <= 0 || videoH <= 0 || box.W <= 0 || box.H <= 0 {
		return 0, 0, 0, 0
	}
	scale := min(box.W/float32(videoW), box.H/float32(videoH))
	w, h = float32(videoW)*scale, float32(videoH)*scale
	return (box.W - w) / 2, (box.H - h) / 2, w, h
}

// MapPointerToVideo maps local element coordinates `(x, y)` inside box into clamped
// `[0..videoW-1, 0..videoH-1]` pixels and reports whether `(x, y)` fell inside the image.
func MapPointerToVideo(x, y float32, box ui.Rect, videoW, videoH int) (vx, vy int32, inside bool) {
	ox, oy, w, h := FitBox(box, videoW, videoH)
	if w <= 0 || h <= 0 || videoW <= 0 || videoH <= 0 {
		return 0, 0, false
	}
	nx := (x - ox) / w
	ny := (y - oy) / h
	inside = nx >= 0 && nx <= 1 && ny >= 0 && ny <= 1
	if nx < 0 {
		nx = 0
	} else if nx > 1 {
		nx = 1
	}
	if ny < 0 {
		ny = 0
	} else if ny > 1 {
		ny = 1
	}
	vx = int32(math.Round(float64(nx) * float64(videoW-1)))
	vy = int32(math.Round(float64(ny) * float64(videoH-1)))
	if vx < 0 {
		vx = 0
	} else if vx >= int32(videoW) {
		vx = int32(videoW - 1)
	}
	if vy < 0 {
		vy = 0
	} else if vy >= int32(videoH) {
		vy = int32(videoH - 1)
	}
	return vx, vy, inside
}

// MapMyGoButton converts a MyGo pointer button index into an Android MotionEvent button flag.
func MapMyGoButton(btn int) uint32 {
	switch btn {
	case 0:
		return scrcpy.ButtonPrimary
	case 1:
		return scrcpy.ButtonSecondary
	case 2:
		return scrcpy.ButtonTertiary
	default:
		return 0
	}
}

// MapMyGoMods converts MyGo keyboard modifiers into Android KeyEvent META_* flags.
func MapMyGoMods(m ui.Modifiers) uint32 {
	var meta uint32
	if m&ui.Shift != 0 {
		meta |= scrcpy.MetaShiftOn
	}
	if m&ui.Alt != 0 {
		meta |= scrcpy.MetaAltOn
	}
	if m&ui.Ctrl != 0 {
		meta |= scrcpy.MetaCtrlOn
	}
	if m&ui.Super != 0 {
		meta |= scrcpy.MetaMetaOn
	}
	return meta
}

var mygoKeyTable = map[ui.Key]struct {
	code      uint32
	printable bool
}{
	ui.KeyEnter:        {scrcpy.KeycodeEnter, false},
	ui.KeyEscape:       {scrcpy.KeycodeEscape, false},
	ui.KeyBackspace:    {scrcpy.KeycodeDel, false},
	ui.KeyDelete:       {scrcpy.KeycodeForwardDel, false},
	ui.KeyInsert:       {scrcpy.KeycodeInsert, false},
	ui.KeyTab:          {scrcpy.KeycodeTab, false},
	ui.KeyHome:         {scrcpy.KeycodeMoveHome, false},
	ui.KeyEnd:          {scrcpy.KeycodeMoveEnd, false},
	ui.KeyPageUp:       {scrcpy.KeycodePageUp, false},
	ui.KeyPageDown:     {scrcpy.KeycodePageDown, false},
	ui.KeyLeft:         {scrcpy.KeycodeDPadLeft, false},
	ui.KeyRight:        {scrcpy.KeycodeDPadRight, false},
	ui.KeyUp:           {scrcpy.KeycodeDPadUp, false},
	ui.KeyDown:         {scrcpy.KeycodeDPadDown, false},
	ui.KeyBack:         {scrcpy.KeycodeBack, false},
	ui.KeySpace:        {scrcpy.KeycodeSpace, false}, // Sent as KeyEvent; trailing " " InputText suppressed.
	ui.KeyMinus:        {scrcpy.KeycodeMinus, true},
	ui.KeyEqual:        {scrcpy.KeycodeEquals, true},
	ui.KeyComma:        {scrcpy.KeycodeComma, true},
	ui.KeyPeriod:       {scrcpy.KeycodePeriod, true},
	ui.KeySlash:        {scrcpy.KeycodeSlash, true},
	ui.KeySemicolon:    {scrcpy.KeycodeSemicolon, true},
	ui.KeyQuote:        {scrcpy.KeycodeApostrophe, true},
	ui.KeyBracketLeft:  {scrcpy.KeycodeLeftBrack, true},
	ui.KeyBracketRight: {scrcpy.KeycodeRightBrack, true},
	ui.KeyBackslash:    {scrcpy.KeycodeBackslash, true},
	ui.KeyBackquote:    {scrcpy.KeycodeGrave, true},
}

func init() {
	letters := []ui.Key{
		ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI,
		ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM, ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR,
		ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ,
	}
	for i, k := range letters {
		mygoKeyTable[k] = struct {
			code      uint32
			printable bool
		}{scrcpy.KeycodeA + uint32(i), true}
	}
	digits := []ui.Key{
		ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4,
		ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9,
	}
	for i, k := range digits {
		mygoKeyTable[k] = struct {
			code      uint32
			printable bool
		}{scrcpy.Keycode0 + uint32(i), true}
	}
	fkeys := []ui.Key{
		ui.KeyF1, ui.KeyF2, ui.KeyF3, ui.KeyF4, ui.KeyF5, ui.KeyF6,
		ui.KeyF7, ui.KeyF8, ui.KeyF9, ui.KeyF10, ui.KeyF11, ui.KeyF12,
	}
	for i, k := range fkeys {
		mygoKeyTable[k] = struct {
			code      uint32
			printable bool
		}{scrcpy.KeycodeF1 + uint32(i), false}
	}
}

// MapMyGoKey converts a ui.Key into its Android keycode and reports whether plain
// unmodified presses should defer to InputText for layout/IME fidelity.
func MapMyGoKey(k ui.Key) (keycode uint32, deferToText bool, ok bool) {
	entry, found := mygoKeyTable[k]
	return entry.code, entry.printable, found
}

// Translate translates one MyGo ui.InputEvent inside video surface box (videoW x videoH).
func (t *InputTracker) Translate(ev ui.InputEvent, box ui.Rect, videoW, videoH int) TranslatedInput {
	if t.heldKeys == nil {
		t.heldKeys = make(map[ui.Key]uint32)
	}
	switch ev.Kind {
	case ui.InputPointerDown:
		vx, vy, inside := MapPointerToVideo(ev.X, ev.Y, box, videoW, videoH)
		if !inside {
			return TranslatedInput{}
		}
		btn := MapMyGoButton(ev.Button)
		if btn == 0 {
			return TranslatedInput{}
		}
		t.buttonsHeld |= btn
		return TranslatedInput{
			Kind:        ActionTouch,
			TouchAction: scrcpy.ActionDown,
			Button:      btn,
			ButtonsHeld: t.buttonsHeld,
			VideoX:      vx,
			VideoY:      vy,
		}
	case ui.InputPointerUp:
		vx, vy, _ := MapPointerToVideo(ev.X, ev.Y, box, videoW, videoH)
		btn := MapMyGoButton(ev.Button)
		t.buttonsHeld &^= btn
		return TranslatedInput{
			Kind:        ActionTouch,
			TouchAction: scrcpy.ActionUp,
			Button:      btn,
			ButtonsHeld: t.buttonsHeld,
			VideoX:      vx,
			VideoY:      vy,
		}
	case ui.InputPointerMove:
		vx, vy, inside := MapPointerToVideo(ev.X, ev.Y, box, videoW, videoH)
		if !inside && t.buttonsHeld == 0 {
			return TranslatedInput{}
		}
		act := scrcpy.ActionHoverMove
		if t.buttonsHeld != 0 {
			act = scrcpy.ActionMove
		}
		return TranslatedInput{
			Kind:        ActionTouch,
			TouchAction: act,
			ButtonsHeld: t.buttonsHeld,
			VideoX:      vx,
			VideoY:      vy,
		}
	case ui.InputScroll:
		vx, vy, inside := MapPointerToVideo(ev.X, ev.Y, box, videoW, videoH)
		if !inside {
			return TranslatedInput{}
		}
		// One Windows mouse notch is ~99 DIPs; positive DY scrolls down content (negative Android vScroll).
		h := float64(-ev.DX) / 99.0
		v := float64(-ev.DY) / 99.0
		if h == 0 && v == 0 {
			return TranslatedInput{}
		}
		return TranslatedInput{
			Kind:        ActionScroll,
			VideoX:      vx,
			VideoY:      vy,
			HScroll:     clampFloat(h, -1, 1),
			VScroll:     clampFloat(v, -1, 1),
			ButtonsHeld: t.buttonsHeld,
		}
	case ui.InputKeyDown:
		if ev.Key == ui.KeyF11 && ev.Mods == 0 {
			return TranslatedInput{Kind: ActionToggleFullscreen}
		}
		if ev.Key == ui.KeyEscape && (ev.Mods&(ui.Ctrl|ui.Alt)) == (ui.Ctrl|ui.Alt) {
			t.buttonsHeld = 0
			clear(t.heldKeys)
			return TranslatedInput{Kind: ActionReleaseFocus, ReleaseFocus: true}
		}
		kc, deferToText, ok := MapMyGoKey(ev.Key)
		if !ok {
			return TranslatedInput{}
		}
		hasShortcutMod := ev.Mods&(ui.Ctrl|ui.Alt|ui.Super) != 0
		if deferToText && !hasShortcutMod {
			return TranslatedInput{}
		}
		if ev.Key == ui.KeySpace && !hasShortcutMod {
			t.suppressSpace = true
		}
		t.heldKeys[ev.Key] = kc
		return TranslatedInput{
			Kind:      ActionKey,
			KeyAction: scrcpy.KeyActionDown,
			Keycode:   kc,
			MetaState: MapMyGoMods(ev.Mods),
		}
	case ui.InputKeyUp:
		kc, ok := t.heldKeys[ev.Key]
		if !ok {
			return TranslatedInput{}
		}
		delete(t.heldKeys, ev.Key)
		return TranslatedInput{
			Kind:      ActionKey,
			KeyAction: scrcpy.KeyActionUp,
			Keycode:   kc,
			MetaState: MapMyGoMods(ev.Mods),
		}
	case ui.InputText:
		if ev.Text == "" {
			return TranslatedInput{}
		}
		if t.suppressSpace && ev.Text == " " {
			t.suppressSpace = false
			return TranslatedInput{}
		}
		t.suppressSpace = false
		if strings.ContainsRune(ev.Text, '\r') || strings.ContainsRune(ev.Text, '\n') {
			return TranslatedInput{}
		}
		return TranslatedInput{
			Kind: ActionText,
			Text: ev.Text,
		}
	}
	return TranslatedInput{}
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
