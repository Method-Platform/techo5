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

	"golang.org/x/image/vector"

	"github.com/HuskerMinion/techo5/echod/internal/feature/rook"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The Rook screen's second page: production's health, a sideways swipe from the sessions, as Rook
// Face's Health screen was on the Show. Six of production's numbers with their last hour, every
// component by tier, the one in trouble spelled out, and the datastores' facts. It goes back to the
// sessions a while after the last touch, and production down puts the sessions page's red takeover up
// whichever page is showing.

// rookHealthFor is how long the Health page stays up after the last touch.
const rookHealthFor = 3 * time.Minute

// rookPager is which of the Rook screen's pages is up, touched from the touch goroutine and read
// from the frame's.
var rookPager struct {
	sync.Mutex
	health bool
	at     time.Time
}

// rookData is what a frame of the Rook screen shows: the bridges' view, and which page.
type rookData struct {
	view   rook.View
	health bool
	focus  rook.FocusResult // the last tap's ask, while it is still worth saying
}

// rookFocusFor is how long the line saying how a tap went stays up.
const rookFocusFor = 6 * time.Second

func rookFill(_ *Display, v *customView) {
	rookPager.Lock()
	if rookPager.health && time.Since(rookPager.at) > rookHealthFor {
		rookPager.health = false
	}
	health := rookPager.health
	rookPager.Unlock()
	d := rookData{view: rook.Snapshot(), health: health}
	if f := rook.LastFocus(); !f.At.IsZero() && time.Since(f.At) < rookFocusFor {
		d.focus = f
	}
	v.data = d
}

// rookGesture is a finger on the Rook screen: sideways turns the page; any touch on Health keeps it.
func rookGesture(_ *Display, g touch.Gesture, at image.Point) {
	rookPager.Lock()
	switch g.Kind {
	case touch.SwipeLeft, touch.SwipeRight:
		rookPager.health = !rookPager.health
	}
	rookPager.at = time.Now()
	rookPager.Unlock()
	if g.Kind != touch.Tap {
		return
	}
	// A tap on a session: bring its terminal forward on its machine.
	z, ok := customZoneAt(at)
	if !ok || z.kind != "session" {
		return
	}
	for _, s := range rook.Snapshot().Sessions {
		if s.Machine+"/"+s.ID == z.id {
			rook.Focus(s)
			return
		}
	}
}

// rookFocusLine is what the screen says about the last tap, and in what color.
func rookFocusLine(f rook.FocusResult) (string, color.RGBA) {
	switch {
	case f.At.IsZero():
		return "", rkInk3
	case !f.Done:
		return "Opening " + f.Label + " on " + f.Machine + "…", rkInk2
	case f.OK:
		return "Brought forward on " + f.Machine, rkGreen
	}
	return "Couldn't open it: " + f.Reason, rkAmber
}

// rookOnHealth is whether this frame is the Health page: asked for, and production not down.
func rookOnHealth(s scene) bool {
	d, ok := s.custom.data.(rookData)
	if !ok || !d.health {
		return false
	}
	_, _, incident := rookIncident(d.view)
	return !incident
}

// rookHealthKey is what a frame of the Health page shows.
func rookHealthKey(v rook.View) string {
	var b strings.Builder
	b.WriteString("health|")
	p := v.Production
	if p == nil {
		return b.String()
	}
	fmt.Fprintf(&b, "%s|%d|", p.Status, len(p.Alerts))
	d := p.Detail
	if d == nil {
		return b.String()
	}
	for _, k := range d.KPIs {
		fmt.Fprintf(&b, "%s=%s:%d", k.ID, k.Value, len(k.Points))
		if n := len(k.Points); n > 0 && k.Points[n-1] != nil {
			fmt.Fprintf(&b, ":%g", *k.Points[n-1])
		}
		b.WriteByte('|')
	}
	for _, c := range d.Components {
		fmt.Fprintf(&b, "%s=%s|", c.Name, c.Status)
	}
	fmt.Fprintf(&b, "%+v|%+v|%+v|%v|%v|%v", d.Queues, d.Errors, d.Integrations, d.Mongo != nil, d.Redis != nil, d.Solr != nil)
	if d.Redis != nil {
		fmt.Fprintf(&b, "|%d", d.Redis.UsedMB/100)
	}
	if len(d.Releases) > 0 {
		fmt.Fprintf(&b, "|%s%d", d.Releases[0].Release, d.Releases[0].AgoSec/3600)
	}
	return b.String()
}

// rookPageDots are the two dots under both pages that say there is another a swipe away.
func (p strip) rookPageDots(health bool) {
	const y = 386.0
	off, on := fade(rkInk3, 0.45), rkInk2
	if health {
		p.dot(p.fx(386), p.fy(y), p.px(2.4), off)
		p.r.roundFill(image.Rect(p.x(393), p.y(y-2.4), p.x(408), p.y(y+2.4)), p.px(2.4), on, on)
	} else {
		p.r.roundFill(image.Rect(p.x(380), p.y(y-2.4), p.x(395), p.y(y+2.4)), p.px(2.4), on, on)
		p.dot(p.fx(402), p.fy(y), p.px(2.4), off)
	}
}

// panel is a rounded card from x0, y0 to x1, y1 dp, in the design's raised ground.
func (p strip) panel(x0, y0, x1, y1 float64, top, bottom, edge color.RGBA) {
	box := image.Rect(p.x(x0), p.y(y0), p.x(x1), p.y(y1))
	p.r.roundFill(box, p.px(11), top, bottom)
	p.r.roundStroke(box, p.px(11), math.Max(1, p.px(0.9)), edge)
}

// rkMix is c at opacity a over g.
func rkMix(c, g color.RGBA, a float64) color.RGBA {
	m := func(f, g uint8) uint8 { return uint8(float64(g) + (float64(f)-float64(g))*a) }
	return color.RGBA{m(c.R, g.R), m(c.G, g.G), m(c.B, g.B), 0xff}
}

var (
	rkCard    = color.RGBA{0x10, 0x13, 0x18, 0xff}
	rkCardLow = color.RGBA{0x0b, 0x0d, 0x11, 0xff}
	rkSky     = color.RGBA{0x7f, 0xa7, 0xd9, 0xff}
)

// spark draws one KPI's last hour in the box x0, y0 to x1, y1 dp: a line with a faint fill under it,
// broken where the series has gaps, and a dot on its newest point.
func (p strip) spark(pts []*float64, x0, y0, x1, y1 float64, c color.RGBA, under color.Color) {
	box := image.Rect(p.x(x0), p.y(y0)-2, p.x(x1)+2, p.y(y1)+2)
	w, h := p.px(x1-x0), p.px(y1-y0)
	lo, hi, n := math.Inf(1), math.Inf(-1), 0
	for _, v := range pts {
		if v != nil {
			lo, hi, n = math.Min(lo, *v), math.Max(hi, *v), n+1
		}
	}
	if n < 2 {
		// No reading: a dotted floor, so the card does not look broken.
		for x := 0.0; x < x1-x0; x += 6 {
			p.r.fillRect(image.Rect(p.x(x0+x), p.y(y1)-1, p.x(x0+x+2), p.y(y1)), fade(rkInk3, 0.5))
		}
		return
	}
	at := func(i int, v float64) (float32, float32) {
		x := float64(i) * w / float64(len(pts)-1)
		y := h - 1.5
		if hi-lo > 1e-9 {
			y = h - 1.5 - (v-lo)/(hi-lo)*(h-4)
		}
		return float32(x), float32(y + 2)
	}
	var runs [][][2]float32
	var cur [][2]float32
	for i, v := range pts {
		if v == nil {
			if len(cur) > 0 {
				runs = append(runs, cur)
				cur = nil
			}
			continue
		}
		x, y := at(i, *v)
		cur = append(cur, [2]float32{x, y})
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	floor := float32(h + 2)
	p.r.fillShape(box, under, func(z *vector.Rasterizer, _, _ float32) {
		for _, run := range runs {
			z.MoveTo(run[0][0], floor)
			for _, q := range run {
				z.LineTo(q[0], q[1])
			}
			z.LineTo(run[len(run)-1][0], floor)
			z.ClosePath()
		}
	})
	half := float32(math.Max(0.8, p.px(0.75)))
	p.r.fillShape(box, c, func(z *vector.Rasterizer, _, _ float32) {
		for _, run := range runs {
			for i := 1; i < len(run); i++ {
				a, b := run[i-1], run[i]
				dx, dy := b[0]-a[0], b[1]-a[1]
				l := float32(math.Hypot(float64(dx), float64(dy)))
				if l == 0 {
					continue
				}
				nx, ny := -dy/l*half, dx/l*half
				z.MoveTo(a[0]+nx, a[1]+ny)
				z.LineTo(b[0]+nx, b[1]+ny)
				z.LineTo(b[0]-nx, b[1]-ny)
				z.LineTo(a[0]-nx, a[1]-ny)
				z.ClosePath()
			}
		}
	})
	last := runs[len(runs)-1]
	q := last[len(last)-1]
	p.dot(float64(box.Min.X)+float64(q[0]), float64(box.Min.Y)+float64(q[1]), p.px(2.2), c)
}

// drawRookHealth draws the Health page.
func drawRookHealth(r *renderer, s scene, v rook.View) []customZone {
	k := math.Min(float64(r.w)/788, float64(r.h)/394)
	p := strip{r: r, k: k, ox: (float64(r.w) - 788*k) / 2, oy: (float64(r.h) - 394*k) / 2}
	r.fillRect(image.Rect(0, 0, r.w, r.h), rkGround)
	p.rookPageDots(true)

	pr := v.Production
	var d *rook.Detail
	if pr != nil {
		d = pr.Detail
	}
	bad := 0
	if d != nil {
		for _, c := range d.Components {
			if c.Status != "healthy" && c.Status != "unknown" {
				bad++
			}
		}
	}

	// The header: the dot, the name, production's word and its alerts, three counts on the right.
	tone := rkGreen
	switch {
	case pr == nil:
		tone = rkInk3
	case len(pr.Alerts) > 0 || bad > 0:
		tone = rkAmber
	}
	p.dot(p.fx(24), p.fy(24), p.px(7.5), fade(tone, 0.18))
	p.dot(p.fx(24), p.fy(24), p.px(4.2), tone)
	p.spaced(p.sans(true, 12.5), "METHOD PLATFORM", 38, 28.5, 2.4, -1, rkInk)
	x := 38 + float64(r.width(p.sans(true, 12.5), "METHOD PLATFORM"))/k + 14*2.4 + 12
	chip := func(word string, c color.RGBA) {
		f := p.sans(true, 9)
		w := float64(r.width(f, word))/k + 9*1.1 + 16
		box := image.Rect(p.x(x), p.y(15), p.x(x+w), p.y(33))
		g := fade(c, 0.13)
		r.roundFill(box, p.px(9), g, g)
		p.spaced(f, word, x+w/2, 27.5, 1.1, 0, c)
		x += w + 7
	}
	switch {
	case pr == nil:
		chip("NO REPORT", rkInk3)
	default:
		word := strings.ToUpper(pr.Status)
		if word == "" {
			word = "—"
		}
		chip(word, tone)
		if n := len(pr.Alerts); n > 0 {
			chip(fmt.Sprintf("%d ALERT%s", n, map[bool]string{true: "S"}[n > 1]), rkAmber)
		}
	}
	if d != nil {
		type count struct {
			label  string
			ok, of int
		}
		counts := []count{
			{"INTEGRATIONS", d.Integrations.Total - d.Integrations.Unhealthy, d.Integrations.Total},
			{"QUEUES", d.Queues.Total - d.Queues.Unhealthy, d.Queues.Total},
			{"COMPONENTS", len(d.Components) - bad, len(d.Components)},
		}
		right := 766.0
		for _, c := range counts {
			c2 := rkInk
			if c.ok < c.of {
				c2 = rkAmber
			}
			lf := p.sans(true, 7)
			lw := float64(r.width(lf, c.label))/k + float64(len(c.label)-1)*1.3
			p.line(p.sans(true, 13.5), fmt.Sprintf("%d/%d", c.ok, c.of), right, 24, 90, 1, c2)
			p.spaced(lf, c.label, right, 36, 1.3, 1, rkInk3)
			right -= math.Max(lw, 40) + 20
		}
	}
	r.fillRect(image.Rect(0, p.y(48), r.w, p.y(48)+max(1, int(p.px(0.9)))), rkRule)

	if d == nil {
		word, sub := "NO HEALTH REPORT", "the bridge has no snapshot from production yet"
		if pr != nil && !pr.Reachable {
			word, sub = "UNREACHABLE", "production did not answer the bridge"
		}
		p.spaced(p.mono(false, 10), word, 394, 205, 1.8, 0, rkInk3)
		p.line(p.sans(false, 13), sub, 394, 228, 600, 0, rkInk2)
		return nil
	}

	// The numbers: three across, two down.
	const gx0, gy0, cw, ch, gap = 16.0, 60.0, 154.0, 150.0, 10.0
	for i, kpi := range d.KPIs {
		if i >= 6 {
			break
		}
		x0 := gx0 + float64(i%3)*(cw+gap)
		y0 := gy0 + float64(i/3)*(ch+gap)
		p.panel(x0, y0, x0+cw, y0+ch, rkCard, rkCardLow, rkRule)
		p.spaced(p.sans(true, 7.5), r.fit(p.sans(true, 7.5), strings.ToUpper(kpi.Label), int(p.px(cw-34))), x0+12, y0+20, 1.2, -1, rkInk3)
		val, unit := kpi.Value, ""
		if kpi.Unit != nil {
			switch *kpi.Unit {
			case "ms":
				val, unit = strings.TrimSuffix(val, " ms"), "ms"
			case "perMin":
				if strings.HasSuffix(val, "/min") {
					val, unit = strings.TrimSuffix(val, "/min"), "/min"
				} else if val != "—" && val != "" {
					unit = "/min"
				}
			}
		}
		big := p.sans(true, 25)
		p.line(big, val, x0+12, y0+50, cw-24, -1, rkInk)
		if unit != "" {
			p.line(p.sans(false, 10.5), unit, x0+14+float64(r.width(big, val))/k, y0+50, 50, -1, rkInk3)
		}
		c := rkSky
		dc := rkInk3
		if kpi.Tone != nil {
			switch *kpi.Tone {
			case "bad":
				c, dc = rkAmber, rkAmber
			case "good":
				c = rkGreen
			}
		}
		p.spark(kpi.Points, x0+12, y0+66, x0+cw-12, y0+120, c, color.NRGBA{c.R, c.G, c.B, 0x2c})
		delta := "last hour"
		if kpi.Delta != nil && *kpi.Delta != "" {
			delta = *kpi.Delta
		}
		p.line(p.sans(false, 8.5), delta, x0+12, y0+139, cw-24, -1, dc)
	}

	// The rail: what is wrong, the tiers, the datastores.
	const rx0, rx1 = 512.0, 772.0
	var trouble []rook.Component
	for _, c := range d.Components {
		if c.Status != "healthy" && c.Status != "unknown" {
			trouble = append(trouble, c)
		}
	}
	if len(trouble) > 0 {
		t := trouble[0]
		c := rkAmber
		if t.Status == "down" || t.Status == "unhealthy" {
			c = rkRed
		}
		p.panel(rx0, 60, rx1, 116, rkMix(c, rkGround, 0.16), rkMix(c, rkGround, 0.06), rkMix(c, rkGround, 0.42))
		p.dot(p.fx(rx0+15), p.fy(78), p.px(3), c)
		p.line(p.sans(true, 12), t.Name, rx0+24, 82, 150, -1, rkInk)
		p.spaced(p.sans(true, 7.5), strings.ToUpper(t.Status), rx1-12, 81, 1.2, 1, c)
		reason := ""
		if t.Reason != nil {
			reason = rookReason(*t.Reason)
		}
		if n := len(trouble); n > 1 {
			reason = fmt.Sprintf("+%d more · ", n-1) + reason
		}
		p.line(p.sans(false, 9), reason, rx0+12, 102, rx1-rx0-24, -1, rkMix(c, rkInk, 0.45))
	} else {
		p.panel(rx0, 60, rx1, 116, rkMix(rkGreen, rkGround, 0.10), rkMix(rkGreen, rkGround, 0.04), rkMix(rkGreen, rkGround, 0.3))
		p.dot(p.fx(rx0+15), p.fy(78), p.px(3), rkGreen)
		p.line(p.sans(true, 12), "Every component healthy", rx0+24, 82, 200, -1, rkInk)
		sub := fmt.Sprintf("%d components · %d queues · %d integrations", len(d.Components), d.Queues.Total, d.Integrations.Total)
		p.line(p.sans(false, 9), sub, rx0+12, 102, rx1-rx0-24, -1, rkInk3)
	}

	p.panel(rx0, 126, rx1, 226, rkRow, rkRow, rkRule)
	for i, g := range []struct{ id, label string }{{"application", "APP"}, {"data", "DATA"}, {"workers", "WORKERS"}, {"edge", "EDGE"}} {
		y := 147 + float64(i)*21
		p.spaced(p.sans(true, 7.5), g.label, rx0+14, y+3, 1.2, -1, rkInk3)
		ok, n := 0, 0
		bx := rx0 + 72
		for _, c := range d.Components {
			if c.Group != g.id {
				continue
			}
			n++
			col := fade(rkGreen, 0.85)
			switch c.Status {
			case "healthy":
				ok++
			case "unknown":
				col = rkIdle
			case "down", "unhealthy":
				col = rkRed
			default:
				col = rkAmber
			}
			if bx+18 <= rx1-40 {
				if col == rkAmber || col == rkRed {
					b := fade(col, 0.25)
					r.roundFill(image.Rect(p.x(bx-2), p.y(y-5), p.x(bx+20), p.y(y+5)), p.px(5), b, b)
				}
				r.roundFill(image.Rect(p.x(bx), p.y(y-3), p.x(bx+18), p.y(y+3)), p.px(3), col, col)
			}
			bx += 22
		}
		c := rkInk2
		if ok < n {
			c = rkAmber
		}
		p.line(p.sans(false, 9.5), fmt.Sprintf("%d/%d", ok, n), rx1-14, y+3.5, 40, 1, c)
	}

	p.panel(rx0, 236, rx1, 372, rkRow, rkRow, rkRule)
	type fact struct {
		label, value string
		ok           bool
	}
	var facts []fact
	if m := d.Mongo; m != nil {
		v := fmt.Sprintf("%d nodes", m.Members)
		ok := m.PrimaryOK
		if m.MaxLagSec != nil {
			v += fmt.Sprintf(" · lag %ds", *m.MaxLagSec)
			ok = ok && *m.MaxLagSec < 10
		}
		facts = append(facts, fact{"MONGO", v, ok})
	}
	if rd := d.Redis; rd != nil {
		facts = append(facts, fact{"REDIS", fmt.Sprintf("%.1f / %.0f GB", float64(rd.UsedMB)/1024, float64(rd.MaxMB)/1024),
			rd.Evicted == 0 && (rd.MaxMB == 0 || float64(rd.UsedMB)/float64(rd.MaxMB) < 0.9)})
	}
	if so := d.Solr; so != nil {
		facts = append(facts, fact{"SOLR", fmt.Sprintf("%.1fM docs", float64(so.Documents)/1e6), so.Up && so.Unhealthy == 0})
	}
	facts = append(facts, fact{"QUEUES", fmt.Sprintf("%d delayed · %d failed", d.Queues.Delayed, d.Queues.FailedLastHour), d.Queues.FailedLastHour == 0 && d.Queues.Unhealthy == 0})
	facts = append(facts, fact{"ERRORS", fmt.Sprintf("%d this hour", d.Errors.Count), d.Errors.Count == 0})
	if len(d.Releases) > 0 {
		rel := d.Releases[0]
		facts = append(facts, fact{strings.ToUpper(rel.App), rel.Release + " · " + rookAgo(s.now, s.now.Add(-time.Duration(rel.AgoSec)*time.Second).UnixMilli()) + " ago", true})
	}
	for i, f := range facts {
		if i >= 6 {
			break
		}
		x := rx0 + 14 + float64(i%2)*124
		y := 258 + float64(i/2)*40
		p.spaced(p.sans(true, 7), f.label, x, y, 1.2, -1, rkInk3)
		c := rkInk2
		if !f.ok {
			c = rkAmber
		}
		p.line(p.sans(false, 10.5), f.value, x, y+16, 114, -1, c)
	}
	return nil
}

// rookReason is a component's reason cut to what fits a line: the task scheduler's sync failures
// summed per direction, anything else as it came.
func rookReason(s string) string {
	if !strings.HasPrefix(s, "Sync failures:") {
		return s
	}
	parts := strings.Split(strings.TrimPrefix(s, "Sync failures:"), "·")
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		words := strings.Fields(part)
		if len(words) == 0 {
			continue
		}
		if words[0] == "outbound" || words[0] == "inbound" {
			n := 0
			for _, w := range words {
				var x int
				if _, err := fmt.Sscanf(w, "%d", &x); err == nil {
					n += x
				}
			}
			out = append(out, fmt.Sprintf("%s %d", words[0], n))
		} else {
			out = append(out, part)
		}
	}
	return "Sync failures · " + strings.Join(out, " · ")
}
