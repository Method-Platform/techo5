//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/vector"

	"github.com/HuskerMinion/techo5/echod/internal/feature/rook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// The Rook screen (Method-Platform fork): the Claude Code sessions the Rook bridges report, drawn as
// Rook Face's "Strip" for the Echo Show 5 (Method-Platform/android-status-monitor,
// design/rook-show5.html): the session that matters most as a disc on the left, the next four as rows
// beside it, and six facts under them. A custom screen (custom_screens.go) in a file of its own, so a
// merge from upstream never touches it; offered only on a device with bridges listed in rook.json.
//
// It keeps its own look rather than the theme's: near-black ground, Inter and Go Mono, and the status
// colors Rook Face uses on every panel, so the Show reads the same as the Portals and the Spot.

// Registered from a package variable, not init: display.go's init builds the display, and with it
// Idle screen's choices, and a file's init runs in file-name order, after display.go's. Every package
// variable is set before any init runs.
var _ = func() bool {
	registerScreen(customScreen{
		id:      "rook",
		label:   "Rook",
		rank:    50,
		offered: rook.Configured,
		listen:  rook.Start,
		fill:    rookFill,
		draw:    drawRook,
		gesture: rookGesture,
		key:     rookKey,
	})
	return true
}()

// Rook Face's palette (design/rook-show5.html).
var (
	rkGround  = color.RGBA{0x07, 0x08, 0x0b, 0xff}
	rkRow     = color.RGBA{0x0e, 0x10, 0x14, 0xff}
	rkRowWait = color.RGBA{0x1b, 0x14, 0x08, 0xff}
	rkRule    = color.RGBA{0x1a, 0x1d, 0x23, 0xff}
	rkInk     = color.RGBA{0xe9, 0xe5, 0xde, 0xff}
	rkInk2    = color.RGBA{0xa6, 0xa2, 0x9b, 0xff}
	rkInk3    = color.RGBA{0x7c, 0x78, 0x71, 0xff}
	rkIdle    = color.RGBA{0x3e, 0x43, 0x4b, 0xff}
	rkGreen   = color.RGBA{0x4f, 0xb8, 0x94, 0xff}
	rkAmber   = color.RGBA{0xf0, 0xa4, 0x2c, 0xff}
	rkRed     = color.RGBA{0xe0, 0x5a, 0x47, 0xff}
	rkRedInk  = color.RGBA{0xf2, 0xb8, 0xae, 0xff}
	rkClay    = color.RGBA{0xd9, 0x77, 0x57, 0xff}
	rkBlue    = color.RGBA{0x4a, 0x6f, 0xa5, 0xff}
	rkViolet  = color.RGBA{0x9b, 0x8a, 0xc4, 0xff}
)

// rookRows is how many sessions are rows beside the disc.
const rookRows = 4

func rookView(s scene) rook.View {
	switch d := s.custom.data.(type) {
	case rookData:
		return d.view
	case rook.View:
		return d
	}
	return rook.View{}
}

// rookKey is what a frame shows, for frames that would look the same. The times in it go by the
// minute, as the frame key's own clock does, and the comet turns with the minute.
func rookKey(s scene) string {
	v := rookView(s)
	var b strings.Builder
	if d, ok := s.custom.data.(rookData); ok && d.sheet != nil {
		line, _ := rookFocusLine(d.focus)
		return rookSheetKey(d.sheet, line)
	}
	if rookOnHealth(s) {
		return rookHealthKey(v)
	}
	if d, ok := s.custom.data.(rookData); ok {
		line, _ := rookFocusLine(d.focus)
		fmt.Fprintf(&b, "focus:%s|", line)
	}
	fmt.Fprintf(&b, "%d/%d|%d|", v.Connected, v.Bridges, s.now.Unix()/60)
	for _, x := range v.Sessions {
		fmt.Fprintf(&b, "%s/%s:%s:%s:%d|", x.Machine, x.ID, x.Status, x.Label(), x.StatusSince/60000)
		if x.Prompt != nil {
			fmt.Fprintf(&b, "prompt:%s:%s|", x.Prompt.ID, x.Prompt.Title)
		}
	}
	if u := v.Usage; u != nil {
		if u.FiveHour != nil {
			fmt.Fprintf(&b, "5h%d|", u.FiveHour.UsedPercent)
		}
		if u.Weekly != nil {
			fmt.Fprintf(&b, "wk%d|", u.Weekly.UsedPercent)
		}
	}
	if c := v.Codex; c != nil && c.Primary != nil {
		fmt.Fprintf(&b, "cx%d|", c.Primary.UsedPercent)
	}
	if c := v.CI; c != nil {
		fmt.Fprintf(&b, "ci%d/%d|", c.Broken, c.Running)
	}
	if p := v.Production; p != nil {
		fmt.Fprintf(&b, "p%v%s%s", p.Reachable, p.Status, strings.Join(p.Alerts, ","))
	}
	return b.String()
}

// rookSummary is a line for what the screen says at a glance, and whether it is urgent.
func rookSummary(v rook.View) (string, bool) {
	switch {
	case v.Bridges > 0 && v.Connected == 0:
		return "No bridge connected", true
	case v.Waiting() > 0:
		return fmt.Sprintf("%d need you · %d working", v.Waiting(), v.Busy()), true
	case v.Busy() > 0:
		return fmt.Sprintf("%d working", v.Busy()), false
	case len(v.Sessions) == 0:
		return "No sessions", false
	}
	return "All idle", false
}

// rookAgo is how long ago sinceMs was, coarsely: "now", "12m", "3h", "2d".
func rookAgo(now time.Time, sinceMs int64) string {
	if sinceMs <= 0 {
		return ""
	}
	d := now.Sub(time.UnixMilli(sinceMs))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// rookClock is how long a session has been as it is: "0:42", "3:05", then coarse past ten hours.
func rookClock(now time.Time, sinceMs int64) string {
	if sinceMs <= 0 {
		return ""
	}
	d := max(now.Sub(time.UnixMilli(sinceMs)), 0)
	if d >= 10*time.Hour {
		return rookAgo(now, sinceMs)
	}
	return fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// rookTone is a session's word and color.
func rookTone(x rook.Session) (string, color.RGBA) {
	switch x.Status {
	case "waiting":
		return "NEEDS YOU", rkAmber
	case "busy":
		return "WORKING", rkGreen
	case "shell":
		return "SHELL", rkBlue
	}
	return "IDLE", rkIdle
}

// rookIncident is production broken badly enough to take the disc: unreachable, or reporting itself
// anything but OK. A standing alert on a production that says OK is the ledger's, not the disc's:
// nothing here acknowledges it, and it would hold the screen red for days.
func rookIncident(v rook.View) (headline, reason string, ok bool) {
	p := v.Production
	if p == nil {
		return "", "", false
	}
	if !p.Reachable {
		return "DOWN", "API unreachable", true
	}
	switch strings.ToLower(p.Status) {
	case "", "ok", "healthy":
		return "", "", false
	}
	reason = strings.ToLower(p.Status)
	if len(p.Alerts) > 0 {
		reason = p.Alerts[0]
	}
	return strings.ToUpper(p.Status), reason, true
}

// The faces, Inter throughout, cached by size.
var (
	rkFaceMu sync.Mutex
	rkFaces  = map[string]font.Face{}
)

func rkFace(family string, bold bool, px float64) font.Face {
	k := fmt.Sprintf("%s/%v/%.1f", family, bold, px)
	rkFaceMu.Lock()
	defer rkFaceMu.Unlock()
	if f, ok := rkFaces[k]; ok {
		return f
	}
	f, err := fonts.Face(family, bold, px)
	if err != nil {
		f, _ = fonts.Face(fonts.Go, bold, px)
	}
	rkFaces[k] = f
	return f
}

// strip is a frame of the Strip: Rook Face's 788 by 394 dp layout, scaled to the panel and centered.
type strip struct {
	r      *renderer
	k      float64 // pixels per dp
	ox, oy float64 // where dp 0,0 is
}

func (p strip) x(dp float64) int      { return int(math.Round(p.ox + dp*p.k)) }
func (p strip) y(dp float64) int      { return int(math.Round(p.oy + dp*p.k)) }
func (p strip) fx(dp float64) float64 { return p.ox + dp*p.k }
func (p strip) fy(dp float64) float64 { return p.oy + dp*p.k }
func (p strip) px(dp float64) float64 { return dp * p.k }

func (p strip) sans(bold bool, dp float64) font.Face { return rkFace("inter", bold, dp*p.k) }

// mono is the design's IBM Plex Mono: Inter stands in, letter-spaced, since Go Mono's slab serifs
// read heavy at these sizes.
func (p strip) mono(bold bool, dp float64) font.Face { return rkFace("inter", bold, dp*p.k*1.06) }

// spaced draws s with track dp between letters, as the design's letter-spaced labels; anchor is
// -1 left, 0 centered, 1 right on x.
func (p strip) spaced(face font.Face, s string, x, y, track float64, anchor int, c color.Color) {
	t := p.px(track)
	w := 0.0
	for _, ch := range s {
		w += float64(p.r.width(face, string(ch))) + t
	}
	w -= t
	at := p.fx(x)
	switch anchor {
	case 0:
		at -= w / 2
	case 1:
		at -= w
	}
	for _, ch := range s {
		p.r.text(face, string(ch), int(math.Round(at)), p.y(y), c)
		at += float64(p.r.width(face, string(ch))) + t
	}
}

// line draws s at x (anchor as spaced), cut to room dp.
func (p strip) line(face font.Face, s string, x, y, room float64, anchor int, c color.Color) {
	s = p.r.fit(face, s, int(p.px(room)))
	w := float64(p.r.width(face, s))
	at := p.fx(x)
	switch anchor {
	case 0:
		at -= w / 2
	case 1:
		at -= w
	}
	p.r.text(face, s, int(math.Round(at)), p.y(y), c)
}

// arc is a stroke along the circle at cx, cy, rad dp, w thick, from degree from to degree to
// clockwise from the top, with round ends.
func (p strip) arc(cx, cy, rad, w, from, to float64, c color.RGBA) {
	X, Y, R, W := p.fx(cx), p.fy(cy), p.px(rad), p.px(w)
	if to-from >= 360 {
		p.r.arcBand(X, Y, R, W, 0, 360, c)
		return
	}
	p.r.arcBand(X, Y, R, W, from, to, c)
	for _, deg := range []float64{from, to} {
		a := (deg - 90) * math.Pi / 180
		p.dot(X+R*math.Cos(a), Y+R*math.Sin(a), W/2, c)
	}
}

// dot is an antialiased disc at pixel x, y.
func (p strip) dot(x, y, rad float64, c color.RGBA) {
	box := image.Rect(int(x-rad)-1, int(y-rad)-1, int(x+rad)+2, int(y+rad)+2)
	ox, oy := x-float64(box.Min.X), y-float64(box.Min.Y)
	p.r.fillShape(box, c, func(z *vector.Rasterizer, _, _ float32) {
		const n = 32
		for i := 0; i <= n; i++ {
			a := 2 * math.Pi * float64(i) / n
			px, py := float32(ox+rad*math.Cos(a)), float32(oy+rad*math.Sin(a))
			if i == 0 {
				z.MoveTo(px, py)
			} else {
				z.LineTo(px, py)
			}
		}
	})
}

// fade is c at opacity a over the ground: the design's translucent strokes, already mixed.
func fade(c color.RGBA, a float64) color.RGBA {
	m := func(f, g uint8) uint8 { return uint8(float64(g) + (float64(f)-float64(g))*a) }
	return color.RGBA{m(c.R, rkGround.R), m(c.G, rkGround.G), m(c.B, rkGround.B), 0xff}
}

// glow floods the disc toward its rim in c, as Rook Face's radial gradient: clear in the middle,
// strongest, edge, at the rim.
func (p strip) glow(cx, cy, rad float64, c color.RGBA, edge float64) {
	const bands, depth = 48, 0.5
	step := rad * depth / bands
	for i := 0; i < bands; i++ {
		t := float64(i) / bands // 0 at the rim
		a := edge * (1 - t) * (1 - t)
		p.r.arcBand(p.fx(cx), p.fy(cy), p.px(rad-step*(float64(i)+0.5)), p.px(step)+1, 0, 360, fade(c, a))
	}
}

// wrap breaks s into at most two lines of room dp in face, the second cut with an ellipsis.
func (p strip) wrap(face font.Face, s string, room float64) []string {
	w := int(p.px(room))
	if p.r.width(face, s) <= w {
		return []string{s}
	}
	words := strings.Fields(s)
	first, i := "", 0
	for ; i < len(words); i++ {
		try := strings.TrimSpace(first + " " + words[i])
		if p.r.width(face, try) > w {
			break
		}
		first = try
	}
	if first == "" { // one long word, as a hyphenated name is: break it where it fits
		n := 0
		for n < len(s) {
			_, size := utf8.DecodeRuneInString(s[n:])
			if p.r.width(face, s[:n+size]) > w {
				break
			}
			n += size
		}
		return []string{s[:n], p.r.fit(face, s[n:], w)}
	}
	return []string{first, p.r.fit(face, strings.Join(words[i:], " "), w)}
}

func drawRook(r *renderer, s scene) []customZone {
	v := rookView(s)
	if d, ok := s.custom.data.(rookData); ok && d.sheet != nil {
		line, c := rookFocusLine(d.focus)
		return drawRookSheet(r, s, d.sheet, line, c)
	}
	if rookOnHealth(s) {
		return drawRookHealth(r, s, v)
	}
	k := math.Min(float64(r.w)/788, float64(r.h)/394)
	p := strip{r: r, k: k, ox: (float64(r.w) - 788*k) / 2, oy: (float64(r.h) - 394*k) / 2}
	r.fillRect(image.Rect(0, 0, r.w, r.h), rkGround)
	var zones []customZone

	head, reason, incident := rookIncident(v)
	var lead *rook.Session
	rest := v.Sessions
	if len(rest) > 0 && !incident {
		lead, rest = &rest[0], rest[1:]
	}

	// The disc.
	const cx, cy, rad = 196.0, 197.0, 162.0
	switch {
	case incident:
		r.roundStroke(image.Rect(0, 0, r.w, r.h).Inset(int(p.px(2))), 0, p.px(3.5), rkRed)
		p.glow(cx, cy, rad, rkRed, 0.30)
		p.arc(cx, cy, rad, 8, 0, 360, rkRed)
		p.spaced(p.mono(false, 10), "V3 PRODUCTION", cx, 163, 1.8, 0, rkRed)
		p.line(p.sans(true, 34), head, cx, 203, 280, 0, rkRedInk)
		p.line(p.sans(false, 14), reason, cx, 227, 260, 0, rkInk)
	case lead != nil:
		word, tone := rookTone(*lead)
		p.arc(cx, cy, rad, 7, 0, 360, rkRule)
		switch lead.Status {
		case "waiting":
			p.glow(cx, cy, rad, rkAmber, 0.24)
			p.arc(cx, cy, rad, 7, 0, 360, rkAmber)
		case "busy":
			// The comet: a bright head and a tail that thins and fades behind it, turning with the
			// minute, so working reads as motion even in a still.
			turn := float64((s.now.Unix()/60)%12) * 30
			for _, seg := range []struct{ from, to, w, a float64 }{
				{218, 242, 3.1, 0.14}, {242, 274, 4.3, 0.30}, {274, 314, 5.6, 0.55}, {314, 360, 7.0, 1},
			} {
				p.arc(cx, cy, rad, seg.w, seg.from+turn, seg.to+turn, fade(rkGreen, seg.a))
			}
		case "shell":
			p.arc(cx, cy, rad, 7, 0, 360, fade(rkBlue, 0.5))
		}
		// The inner arc: the five-hour window used, as on the Spot's face.
		if u := v.Usage; u != nil && u.FiveHour != nil && u.FiveHour.UsedPercent > 0 {
			p.arc(cx, cy, 138.2, 3.5, 0, 360*math.Min(float64(u.FiveHour.UsedPercent)/100, 1), rkClay)
		}
		p.spaced(p.mono(false, 10), word, cx, 152, 1.8, 0, tone)
		big := p.sans(true, 19)
		lines := p.wrap(big, lead.Label(), 236)
		top := 186.0
		if len(lines) == 1 {
			top = 197
		}
		for i, l := range lines {
			p.line(big, l, cx, top+float64(i)*23, 236, 0, rkInk)
		}
		meta := lead.Machine
		if c := rookClock(s.now, lead.StatusSince); c != "" {
			meta += " · " + c
		}
		y := 240.0
		if lead.Prompt != nil {
			p.line(p.sans(false, 12.5), lead.Prompt.Title+" · tap to answer", cx, y, 220, 0, rkAmber)
			y += 20
		} else if lead.Status == "waiting" && lead.WaitingFor != nil && *lead.WaitingFor != "" {
			p.line(p.sans(false, 12.5), *lead.WaitingFor, cx, y, 220, 0, rkAmber)
			y += 20
		}
		p.spaced(p.mono(false, 10.5), meta, cx, y, 0.9, 0, rkInk3)
		zones = append(zones, customZone{r: image.Rect(p.x(cx-rad), p.y(cy-rad), p.x(cx+rad), p.y(cy+rad)), kind: "session", id: lead.Machine + "/" + lead.ID})
	default:
		p.arc(cx, cy, rad, 7, 0, 360, rkRule)
		word, sub := "NO SESSIONS", "nothing running"
		if v.Bridges > 0 && v.Connected == 0 {
			word, sub = "CONNECTING", "to the bridges"
		}
		p.spaced(p.mono(false, 10), word, cx, 190, 1.8, 0, rkInk3)
		p.line(p.sans(false, 14), sub, cx, 214, 220, 0, rkInk2)
	}

	// The rule between the disc and the rest.
	r.fillRect(image.Rect(p.x(392), p.y(26), p.x(392)+max(1, int(p.px(1.2))), p.y(368)), rkRule)

	// The rows.
	shown := rest
	if len(shown) > rookRows {
		shown = shown[:rookRows]
	}
	for i, x := range shown {
		top := 24 + float64(i)*56
		box := image.Rect(p.x(416), p.y(top), p.x(772), p.y(top+50))
		ground := rkRow
		if x.Status == "waiting" {
			ground = rkRowWait
		}
		r.roundFill(box, p.px(8), ground, ground)
		word, tone := rookTone(x)
		p.dot(p.fx(432), p.fy(top+25), p.px(3.6), tone)
		p.line(p.sans(true, 13), x.Label(), 446, top+22, 236, -1, rkInk)
		sub := x.Machine
		if x.Prompt != nil {
			sub = x.Prompt.Title + " · tap to answer · " + x.Machine
		} else if x.Status == "waiting" && x.WaitingFor != nil && *x.WaitingFor != "" {
			sub = *x.WaitingFor + " · " + x.Machine
		}
		small := p.mono(false, 9)
		p.spaced(small, r.fit(small, sub, int(p.px(215))), 446, top+38, 0.6, -1, rkInk3)
		p.spaced(p.mono(false, 8.5), word, 758, top+22, 1, 1, tone)
		age := rookAgo(s.now, x.StatusSince)
		if x.Status == "waiting" || x.Status == "busy" {
			age = rookClock(s.now, x.StatusSince)
		}
		p.line(p.mono(false, 11), age, 758, top+38, 80, 1, rkInk2)
		zones = append(zones, customZone{r: box, kind: "session", id: x.Machine + "/" + x.ID})
	}

	// The ledger: six facts, three across.
	r.fillRect(image.Rect(p.x(416), p.y(258), p.x(772), p.y(258)+max(1, int(p.px(1.2)))), rkRule)
	pct := func(w *rook.Window, c color.RGBA) (string, color.RGBA) {
		if w == nil {
			return "—", rkInk3
		}
		if w.UsedPercent >= 90 {
			c = rkRed
		}
		return fmt.Sprintf("%d%%", w.UsedPercent), c
	}
	var five, week *rook.Window
	if u := v.Usage; u != nil {
		five, week = u.FiveHour, u.Weekly
	}
	type fact struct {
		label, value string
		c            color.RGBA
	}
	var facts [6]fact
	facts[0].label, facts[1].label, facts[2].label = "SESSION", "WEEK", "CODEX"
	facts[3].label, facts[4].label, facts[5].label = "PROD", "CI", "MACHINES"
	facts[0].value, facts[0].c = pct(five, rkClay)
	facts[1].value, facts[1].c = pct(week, rkBlue)
	facts[2].value, facts[2].c = "—", rkInk3
	if c := v.Codex; c != nil && c.Primary != nil {
		facts[2].value, facts[2].c = pct(&rook.Window{UsedPercent: c.Primary.UsedPercent}, rkViolet)
	}
	facts[3].value, facts[3].c = "—", rkInk3
	if pr := v.Production; pr != nil {
		switch {
		case incident:
			facts[3].value, facts[3].c = head, rkRed
		case len(pr.Alerts) == 1:
			facts[3].value, facts[3].c = "1 ALERT", rkAmber
		case len(pr.Alerts) > 1:
			facts[3].value, facts[3].c = fmt.Sprintf("%d ALERTS", len(pr.Alerts)), rkAmber
		default:
			facts[3].value, facts[3].c = "OK", rkGreen
		}
	}
	facts[4].value, facts[4].c = "—", rkInk3
	if ci := v.CI; ci != nil {
		switch {
		case ci.Broken > 0:
			facts[4].value, facts[4].c = fmt.Sprintf("%d RED", ci.Broken), rkRed
		case ci.Running > 0:
			facts[4].value, facts[4].c = fmt.Sprintf("%d RUN", ci.Running), rkBlue
		default:
			facts[4].value, facts[4].c = "OK", rkGreen
		}
	}
	facts[5].value, facts[5].c = fmt.Sprintf("%d/%d", v.Connected, v.Bridges), rkGreen
	switch {
	case v.Connected == 0:
		facts[5].c = rkRed
	case v.Connected < v.Bridges:
		facts[5].c = rkAmber
	}
	for i, f := range facts {
		x := 416 + float64(i%3)*118.67
		y := 280 + float64(i/3)*42
		p.spaced(p.mono(false, 8.5), f.label, x, y, 1.2, -1, rkInk3)
		size := 17.0
		if utf8.RuneCountInString(f.value) > 6 {
			size = 13
		}
		p.line(p.mono(true, size), f.value, x, y+18, 112, -1, f.c)
	}

	p.rookPageDots(false)
	if d, ok := s.custom.data.(rookData); ok {
		if line, c := rookFocusLine(d.focus); line != "" {
			p.line(p.sans(false, 9.5), line, 416, 381, 290, -1, c)
		}
	}
	if more := len(rest) - len(shown); more > 0 {
		p.spaced(p.mono(false, 8.5), fmt.Sprintf("+%d MORE", more), 772, 380, 1.3, 1, rkInk3)
	}
	return zones
}
