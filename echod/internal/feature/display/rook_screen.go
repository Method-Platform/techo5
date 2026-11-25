//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/rook"
)

// The Rook screen (Method-Platform fork): the Claude Code sessions the Rook bridges report, which need
// someone and which are working, beside the usage windows and production's status. A custom screen
// (custom_screens.go) in a file of its own, so a merge from upstream never touches it; offered only on
// a device with bridges listed in rook.json (feature/rook).

func init() {
	registerScreen(customScreen{
		id:      "rook",
		label:   "Rook",
		rank:    50,
		offered: rook.Configured,
		listen:  rook.Start,
		fill:    func(_ *Display, v *customView) { v.data = rook.Snapshot() },
		draw:    drawRook,
		key:     rookKey,
	})
}

// The Rook screen's own colors: the status lights. The rest are the theme's.
var (
	rookWaiting = color.RGBA{0xe8, 0x5d, 0x4a, 0xff}
	rookBusy    = color.RGBA{0x4f, 0xb8, 0x7a, 0xff}
)

const (
	rookRowH   = 62  // a session's row
	rookSideW  = 300 // the column at the right: usage and production
	rookMaxRow = 6   // rows drawn before "and N more"
)

func rookView(s scene) rook.View {
	v, _ := s.custom.data.(rook.View)
	return v
}

// rookKey is what a frame shows, for frames that would look the same. The times in it go by the
// minute, as the frame key's own clock does.
func rookKey(s scene) string {
	v := rookView(s)
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d|", v.Connected, v.Bridges)
	for _, x := range v.Sessions {
		fmt.Fprintf(&b, "%s:%s:%d|", x.ID, x.Status, x.StatusSince/60000)
	}
	if u := v.Usage; u != nil {
		if u.FiveHour != nil {
			fmt.Fprintf(&b, "5h%d|", u.FiveHour.UsedPercent)
		}
		if u.Weekly != nil {
			fmt.Fprintf(&b, "wk%d|", u.Weekly.UsedPercent)
		}
	}
	if p := v.Production; p != nil {
		fmt.Fprintf(&b, "p%s%d", p.Status, len(p.Alerts))
	}
	return b.String()
}

// rookSummary is the top bar's line, and whether it is urgent.
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

// rookAgo is how long a session has been as it is: "now", "12m", "3h", "2d".
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

func rookStatusWord(x rook.Session) string {
	switch x.Status {
	case "waiting":
		if x.WaitingFor != nil && *x.WaitingFor != "" {
			return *x.WaitingFor
		}
		return "needs you"
	case "busy":
		return "working"
	}
	return x.Status
}

func drawRook(r *renderer, s scene) []customZone {
	v := rookView(s)
	summary, hot := rookSummary(v)
	r.fillRect(image.Rect(0, 0, r.w, r.h), walnut)
	zones := r.customBar(s, summary, hot, false)
	fc := r.faces()

	top := r.s(customBarH) + r.s(10)
	in := r.s(customIn)
	side := r.s(rookSideW)
	listR := image.Rect(in, top, r.w-side-in*2, r.h-in)
	sideR := image.Rect(r.w-side-in, top, r.w-in, r.h-in)

	// The sessions, one row each.
	rowH := r.s(rookRowH)
	rows := v.Sessions
	more := 0
	if fit := listR.Dy() / rowH; len(rows) > fit && fit > 0 {
		more = len(rows) - (fit - 1)
		rows = rows[:fit-1]
	}
	if len(rows) > rookMaxRow {
		more += len(rows) - rookMaxRow
		rows = rows[:rookMaxRow]
	}
	if len(v.Sessions) == 0 {
		msg := "No Claude Code sessions"
		if v.Bridges > 0 && v.Connected == 0 {
			msg = "Connecting to the bridges…"
		}
		r.text(fc.label, msg, listR.Min.X, listR.Min.Y+r.s(40), dim)
	}
	for i, x := range rows {
		row := image.Rect(listR.Min.X, listR.Min.Y+i*rowH, listR.Max.X, listR.Min.Y+(i+1)*rowH-r.s(6))
		ground, lamp := surface(1), dim
		switch x.Status {
		case "waiting":
			ground, lamp = shift(surface(2), 6), rookWaiting
		case "busy":
			lamp = rookBusy
		}
		rad := float64(r.s(14))
		r.roundFill(row, rad, ground, ground)
		if x.Status == "waiting" {
			r.roundStroke(row, rad, r.sf(2), rookWaiting)
		}
		cy := row.Min.Y + row.Dy()/2
		r.disc(row.Min.X+r.s(24), cy, r.s(8), lamp)

		ago := rookAgo(s.now, x.StatusSince)
		agoW := r.width(fc.value, ago)
		r.text(fc.value, ago, row.Max.X-r.s(16)-agoW, cy+r.s(10), dim)

		nameX := row.Min.X + r.s(46)
		room := row.Max.X - r.s(32) - agoW - nameX
		name := x.Name
		if name == "" {
			name = x.Project
		}
		r.text(fc.labelBold, r.fit(fc.labelBold, name, room), nameX, cy-r.s(2), cream)
		sub := rookStatusWord(x)
		if x.Machine != "" {
			sub += " · " + x.Machine
		}
		subCol := dim
		if x.Status == "waiting" {
			subCol = rookWaiting
		}
		r.text(fc.sub, r.fit(fc.sub, sub, room), nameX, cy+r.s(22), subCol)
		zones = append(zones, customZone{r: row, kind: "session", id: x.ID})
	}
	if more > 0 {
		y := listR.Min.Y + len(rows)*rowH + r.s(30)
		r.text(fc.sub, fmt.Sprintf("and %d more", more), listR.Min.X+r.s(46), y, dim)
	}

	// The side: usage windows, then production.
	y := sideR.Min.Y
	if u := v.Usage; u != nil {
		y = rookMeter(r, sideR, y, "5-hour window", u.FiveHour, s.now)
		y = rookMeter(r, sideR, y, "This week", u.Weekly, s.now)
	}
	if p := v.Production; p != nil {
		y += r.s(10)
		col, word := rookBusy, "Production OK"
		switch {
		case !p.Reachable:
			col, word = rookWaiting, "Production unreachable"
		case len(p.Alerts) > 0:
			col, word = rookWaiting, fmt.Sprintf("Production: %d alert", len(p.Alerts))
			if len(p.Alerts) > 1 {
				word += "s"
			}
		case p.Status != "" && p.Status != "ok" && p.Status != "healthy":
			col, word = amber, "Production "+p.Status
		}
		r.disc(sideR.Min.X+r.s(8), y+r.s(18), r.s(7), col)
		r.text(fc.label, r.fit(fc.label, word, sideR.Dx()-r.s(24)), sideR.Min.X+r.s(24), y+r.s(28), cream)
		if p.Version != "" {
			r.text(fc.sub, p.Version, sideR.Min.X+r.s(24), y+r.s(56), dim)
		}
	}
	return zones
}

// rookMeter draws one usage window as a labeled bar, returning where the next goes.
func rookMeter(r *renderer, side image.Rectangle, y int, label string, w *rook.Window, now time.Time) int {
	if w == nil {
		return y
	}
	fc := r.faces()
	pct := fmt.Sprintf("%d%%", w.UsedPercent)
	r.text(fc.label, label, side.Min.X, y+r.s(26), cream)
	r.text(fc.value, pct, side.Max.X-r.width(fc.value, pct), y+r.s(26), cream)
	bar := image.Rect(side.Min.X, y+r.s(40), side.Max.X, y+r.s(56))
	rad := float64(bar.Dy()) / 2
	r.roundFill(bar, rad, surface(2), surface(2))
	frac := min(max(float64(w.UsedPercent)/100, 0), 1)
	if frac > 0 {
		fill := bar
		fill.Max.X = bar.Min.X + max(int(float64(bar.Dx())*frac), bar.Dy())
		col := amber
		if w.UsedPercent >= 90 {
			col = rookWaiting
		}
		r.roundFill(fill, rad, col, col)
	}
	next := y + r.s(66)
	if w.ResetsAt > 0 {
		left := time.UnixMilli(w.ResetsAt).Sub(now)
		if left > 0 {
			word := "resets in " + rookAgo(now, now.Add(-left).UnixMilli())
			r.text(fc.sub, word, side.Min.X, next+r.s(10), dim)
			next += r.s(30)
		}
	}
	return next + r.s(14)
}
