//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/rook"
)

// The Rook screen's prompt sheet: a session waiting on a permission its bridge holds (rook.Prompt) is
// answered here. A tap on that session opens the sheet over the whole screen: what the tool would do,
// and Deny and Allow, or a question's choices. The terminal shows its own dialog all the while, and
// whichever is answered first wins; the sheet closes itself once the prompt is gone from the bridge,
// or two minutes after the last touch.

// rookSheetFor is how long the sheet stays up after the last touch.
const rookSheetFor = 2 * time.Minute

// rookSheetSession is the session whose prompt the sheet shows, from the view: nil when the sheet is
// not up, or its prompt has been answered (here or elsewhere).
func rookSheetSession(v rook.View, key string) *rook.Session {
	if key == "" {
		return nil
	}
	for i := range v.Sessions {
		s := &v.Sessions[i]
		if s.Machine+"/"+s.ID == key && s.Prompt != nil {
			return s
		}
	}
	return nil
}

// rookSheetKey is what a frame of the sheet shows.
func rookSheetKey(s *rook.Session, line string) string {
	p := s.Prompt
	return fmt.Sprintf("sheet|%s/%s|%s|%v|%v|%s", s.Machine, s.ID, p.ID, p.Answerable, rook.CanAnswer(*s), line)
}

// rookSheetAnswerable is whether the sheet offers answers, and if not, why.
func rookSheetAnswerable(s rook.Session) (bool, string) {
	p := s.Prompt
	switch {
	case !rook.CanAnswer(s):
		return false, "This display holds no answer token for " + s.Machine + "."
	case !p.Answerable && p.Why != nil && *p.Why != "":
		return false, strings.ToUpper((*p.Why)[:1]) + (*p.Why)[1:] + "."
	case !p.Answerable:
		return false, "Answering from a display is not set up on " + s.Machine + "."
	}
	return true, ""
}

// drawRookSheet draws the sheet for s, the last tap's line under it.
func drawRookSheet(r *renderer, s scene, x *rook.Session, line string, lineC color.RGBA) []customZone {
	k := math.Min(float64(r.w)/788, float64(r.h)/394)
	p := strip{r: r, k: k, ox: (float64(r.w) - 788*k) / 2, oy: (float64(r.h) - 394*k) / 2}
	r.fillRect(image.Rect(0, 0, r.w, r.h), rkGround)
	// The amber of a session that needs you, along the top edge.
	for i := 0; i < 24; i++ {
		a := 0.22 * (1 - float64(i)/24) * (1 - float64(i)/24)
		r.fillRect(image.Rect(0, p.y(float64(i)*2), r.w, p.y(float64(i+1)*2)), fade(rkAmber, a))
	}
	var zones []customZone
	pr := x.Prompt

	word := "NEEDS YOUR OK"
	switch pr.Kind {
	case "question":
		word = "ASKS YOU"
	case "plan":
		word = "PLAN READY"
	}
	p.spaced(p.sans(true, 9), word, 26, 34, 1.8, -1, rkAmber)
	p.line(p.sans(true, 17), x.Label(), 26, 60, 520, -1, rkInk)
	p.line(p.sans(false, 10.5), x.Machine+" · "+rookAgo(s.now, x.StatusSince), 26, 78, 300, -1, rkInk3)

	// Open on the computer, and close.
	open := image.Rect(p.x(560), p.y(22), p.x(712), p.y(54))
	r.roundFill(open, p.px(16), rkRow, rkRow)
	r.roundStroke(open, p.px(16), math.Max(1, p.px(0.9)), rkRule)
	p.line(p.sans(true, 10), "Open on computer", 636-float64(r.width(p.sans(true, 10), "Open on computer"))/k/2, 42, 140, -1, rkInk2)
	zones = append(zones, customZone{r: open, kind: "sheet-open"})
	shut := image.Rect(p.x(724), p.y(22), p.x(762), p.y(54))
	r.roundFill(shut, p.px(16), rkRow, rkRow)
	r.roundStroke(shut, p.px(16), math.Max(1, p.px(0.9)), rkRule)
	p.line(p.sans(false, 16), "×", 743-float64(r.width(p.sans(false, 16), "×"))/k/2, 44, 30, -1, rkInk2)
	zones = append(zones, customZone{r: shut.Inset(-int(p.px(8))), kind: "sheet-close"})

	// What it would do.
	p.panel(24, 96, 764, 254, rkCard, rkCardLow, rkRule)
	tool := strings.ToUpper(pr.Title)
	tf := p.sans(true, 8.5)
	tw := float64(r.width(tf, tool))/k + float64(len(tool))*1.2 + 18
	chip := image.Rect(p.x(40), p.y(110), p.x(40+tw), p.y(128))
	cg := fade(rkAmber, 0.16)
	r.roundFill(chip, p.px(9), cg, cg)
	p.spaced(tf, tool, 40+tw/2, 122.5, 1.2, 0, rkAmber)
	body := p.sans(false, 14)
	lines := rookWrapN(p, body, pr.Detail, 708, 5)
	for i, l := range lines {
		p.line(body, l, 40, 152+float64(i)*19, 708, -1, rkInk)
	}
	if pr.Note != nil && *pr.Note != "" {
		p.line(p.sans(false, 10.5), *pr.Note, 40, 244, 708, -1, rkInk3)
	}

	// The answers.
	ok, why := rookSheetAnswerable(*x)
	const by0, by1 = 272.0, 344.0
	button := func(x0, x1 float64, label string, fill, edge, ink color.RGBA, id string) {
		b := image.Rect(p.x(x0), p.y(by0), p.x(x1), p.y(by1))
		r.roundFill(b, p.px(14), fill, fill)
		r.roundStroke(b, p.px(14), math.Max(1, p.px(1.2)), edge)
		f := p.sans(true, 17)
		if len(pr.Options) > 2 {
			f = p.sans(true, 13)
		}
		lw := float64(r.width(f, r.fit(f, label, int(p.px(x1-x0-24))))) / k
		p.line(f, label, (x0+x1)/2-lw/2, (by0+by1)/2+6, x1-x0-24, -1, ink)
		zones = append(zones, customZone{r: b, kind: "answer", id: id})
	}
	switch {
	case !ok:
		p.line(p.sans(true, 14), "Answer this one at the computer", 26, 300, 740, -1, rkInk2)
		p.line(p.sans(false, 11), why, 26, 322, 740, -1, rkInk3)
	case pr.Kind == "question" && len(pr.Options) > 0:
		n := float64(len(pr.Options))
		w := (740 - 12*(n-1)) / n
		for i, o := range pr.Options {
			x0 := 24 + float64(i)*(w+12)
			button(x0, x0+w, o, rkRow, fade(rkAmber, 0.7), rkInk, "opt:"+o)
		}
	default:
		allow := "Allow"
		if pr.Kind == "plan" {
			allow = "Approve"
		}
		button(24, 384, "Deny", rkRow, rkRule, rkInk2, "deny")
		button(404, 764, allow, rkMix(rkGreen, rkGround, 0.22), rkGreen, rkInk, "allow")
	}
	if line != "" {
		p.line(p.sans(false, 10), line, 26, 378, 600, -1, lineC)
	} else if ok {
		p.line(p.sans(false, 9.5), "The terminal is asking too; whichever answers first wins.", 26, 378, 600, -1, rkInk3)
	}
	return zones
}

// rookWrapN breaks s into at most n lines of room dp, the last cut with an ellipsis.
func rookWrapN(p strip, face font.Face, s string, room float64, n int) []string {
	w := int(p.px(room))
	var out []string
	rest := strings.Join(strings.Fields(s), " ")
	for len(out) < n && rest != "" {
		if len(out) == n-1 || p.r.width(face, rest) <= w {
			out = append(out, p.r.fit(face, rest, w))
			break
		}
		// The longest prefix that fits, broken at a space when there is one.
		cut := 0
		for i := range rest {
			if i > 0 && p.r.width(face, rest[:i]) > w {
				break
			}
			cut = i
		}
		if sp := strings.LastIndex(rest[:cut], " "); sp > cut/2 {
			cut = sp
		}
		if cut == 0 {
			cut = len(rest)
		}
		out = append(out, strings.TrimSpace(rest[:cut]))
		rest = strings.TrimSpace(rest[cut:])
	}
	return out
}
