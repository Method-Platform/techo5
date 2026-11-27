//go:build !dot && !spot

package display

import (
	"encoding/json"
	"image"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/rook"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

func rookSample(now time.Time) rook.View {
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	asks := "approve Bash"
	title := "Echo Show 5 Rook screen on TECHO5"
	return rook.View{
		Bridges: 3, Connected: 2,
		Sessions: []rook.Session{
			{ID: "a", Name: "mac-mini-orchestrator", Title: &title, Status: "busy", StatusSince: ms(64 * time.Minute), Machine: "mac-mini"},
			{ID: "b", Name: "mac-mini-flutter-dev", Status: "waiting", WaitingFor: &asks, StatusSince: ms(3 * time.Minute), Machine: "mac-mini"},
			{ID: "c", Name: "lead-automations-otto", Status: "busy", StatusSince: ms(40 * time.Second), Machine: "otto-iMac"},
			{ID: "d", Name: "harold-shell", Status: "shell", StatusSince: ms(6 * time.Hour), Machine: "harold-omarchy"},
			{ID: "e", Name: "mp-frontend-otto", Status: "idle", StatusSince: ms(5 * 24 * time.Hour), Machine: "otto-iMac"},
			{ID: "f", Name: "accounting-otto", Status: "idle", StatusSince: ms(6 * 24 * time.Hour), Machine: "otto-iMac"},
		},
		Codex: &rook.Codex{Primary: &struct {
			UsedPercent int `json:"usedPercent"`
		}{83}},
		CI: &rook.CI{},
		Usage: &rook.Usage{
			FiveHour: &rook.Window{UsedPercent: 63, ResetsAt: now.Add(95 * time.Minute).UnixMilli()},
			Weekly:   &rook.Window{UsedPercent: 92, ResetsAt: now.Add(50 * time.Hour).UnixMilli()},
		},
		Production: &rook.Production{Reachable: true, Status: "ok", Version: "v3.18.2"},
	}
}

// The Rook screen draws its sessions on the Show 5 and the Show 8, each row a tap zone, the one waiting
// first; with SHOW_PREVIEW set the frames are written there.
func TestRookScreenDraws(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := customScreens
	t.Cleanup(func() { customScreens = was })
	customScreens = slices.DeleteFunc(slices.Clone(customScreens), func(c customScreen) bool { return c.id != "rook" })
	for i := range customScreens {
		customScreens[i].offered = nil // as if rook.json listed bridges
	}

	at := time.Date(2026, 11, 25, 16, 20, 0, 0, time.Local)
	for _, size := range []struct {
		name string
		w, h int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
		v := rookSample(at)
		s := scene{now: at, phase: "idle", custom: customView{name: "rook", data: v}}
		goldenDraw(t, "rook-"+size.name, size.w, size.h, s)
		customHits.Lock()
		zones := slices.Clone(customHits.zones)
		customHits.Unlock()
		var sessions []string
		for _, z := range zones {
			if z.kind == "session" {
				sessions = append(sessions, z.id[strings.Index(z.id, "/")+1:])
			}
		}
		if !slices.Equal(sessions, []string{"a", "b", "c", "d", "e"}) {
			t.Fatalf("%s: session zones %v", size.name, sessions)
		}
		if rookKey(s) == "" || customFrameKey(s, false, false) == "" {
			t.Fatal("the Rook screen has no frame key")
		}

		empty := scene{now: at, phase: "idle", custom: customView{name: "rook", data: rook.View{Bridges: 2}}}
		goldenDraw(t, "rook-connecting-"+size.name, size.w, size.h, empty)

		waiting := rookSample(at)
		waiting.Sessions[0], waiting.Sessions[1] = waiting.Sessions[1], waiting.Sessions[0]
		goldenDraw(t, "rook-waiting-"+size.name, size.w, size.h, scene{now: at, phase: "idle", custom: customView{name: "rook", data: waiting}})

		down := rookSample(at)
		down.Production = &rook.Production{Reachable: true, Status: "degraded", Alerts: []string{"redis down"}}
		goldenDraw(t, "rook-incident-"+size.name, size.w, size.h, scene{now: at, phase: "idle", custom: customView{name: "rook", data: down}})
	}
	// The Health page, a swipe away, with production's detail as a bridge sends it.
	var detail rook.Detail
	if err := json.Unmarshal([]byte(rookDetailSample), &detail); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
		v := rookSample(at)
		v.Production = &rook.Production{Reachable: true, Status: "OK", Alerts: []string{"Task scheduler degraded"}, Detail: &detail}
		s := scene{now: at, phase: "idle", custom: customView{name: "rook", data: rookData{view: v, health: true}}}
		if !rookOnHealth(s) || !strings.HasPrefix(rookKey(s), "health|") {
			t.Fatal("not the Health page")
		}
		goldenDraw(t, "rook-health-"+size.name, size.w, size.h, s)
		v.Production = &rook.Production{Reachable: true, Status: "OK"}
		goldenDraw(t, "rook-health-none-"+size.name, size.w, size.h, scene{now: at, phase: "idle", custom: customView{name: "rook", data: rookData{view: v, health: true}}})
		down := rookSample(at)
		down.Production = &rook.Production{Reachable: false, Status: "down", Detail: &detail}
		if rookOnHealth(scene{custom: customView{name: "rook", data: rookData{view: down, health: true}}}) {
			t.Fatal("production down left the Health page up")
		}
	}
	rookPager.health = false
	rookGesture(nil, touch.Gesture{Kind: touch.SwipeLeft}, image.Point{})
	if !rookPager.health {
		t.Fatal("a swipe did not turn to Health")
	}
	rookGesture(nil, touch.Gesture{Kind: touch.SwipeRight}, image.Point{})
	if rookPager.health {
		t.Fatal("a swipe did not turn back")
	}
	// A tap's line: asked, then how it went.
	if l, _ := rookFocusLine(rook.FocusResult{Label: "a", Machine: "m", At: at}); l != "Opening a on m…" {
		t.Fatalf("focus line %q", l)
	}
	if l, c := rookFocusLine(rook.FocusResult{Machine: "m", At: at, Done: true, Reason: "no terminal"}); l != "Couldn't open it: no terminal" || c != rkAmber {
		t.Fatalf("focus line %q", l)
	}
	tapped := rookData{view: rookSample(at), focus: rook.FocusResult{Label: "mac-mini-flutter-dev", Machine: "mac-mini", At: at, Done: true, OK: true}}
	goldenDraw(t, "rook-focus-show5", showWide, showHigh, scene{now: at, phase: "idle", custom: customView{name: "rook", data: tapped}})

	// The prompt sheet: a permission, a question, and one that has to be answered at the computer.
	note := "Fetch HTTP headers from example.com"
	for _, c := range []struct {
		name string
		pr   rook.Prompt
	}{
		{"permission", rook.Prompt{ID: "p1", Kind: "permission", Tool: "Bash", Title: "Bash", Answerable: true, Note: &note,
			Detail: "curl -sI https://example.com && rm -rf build/ && go test ./... -run TestRookScreenDraws -count=1 2>&1 | tail -40 && echo done with a long enough line to wrap twice over the card"}},
		{"question", rook.Prompt{ID: "p2", Kind: "question", Tool: "AskUserQuestion", Title: "Branch", Answerable: true,
			Detail: "Which branch should the release go out from?", Options: []string{"v3-final", "main", "development"}}},
		{"elsewhere", rook.Prompt{ID: "p3", Kind: "question", Tool: "AskUserQuestion", Title: "Setup", Answerable: false,
			Detail: "Pick the features to enable"}},
	} {
		v := rookSample(at)
		pr := c.pr
		v.Sessions[1].Prompt = &pr
		sheet := rookSheetSession(v, v.Sessions[1].Machine+"/"+v.Sessions[1].ID)
		if sheet == nil || sheet.Prompt.ID != pr.ID {
			t.Fatal("the sheet did not find its session")
		}
		goldenDraw(t, "rook-sheet-"+c.name+"-show5", showWide, showHigh, scene{now: at, phase: "idle", custom: customView{name: "rook", data: rookData{view: v, sheet: sheet}}})
		if c.name == "permission" {
			goldenDraw(t, "rook-sheet-prompted-show5", showWide, showHigh, scene{now: at, phase: "idle", custom: customView{name: "rook", data: rookData{view: v}}})
		}
	}
	if rookSheetSession(rookSample(at), "mac-mini/b") != nil {
		t.Fatal("a session with no prompt opened the sheet")
	}

	if got := rookReason("Sync failures: outbound leads 5 DUPLICATE, leadNotes 4 X, textMessages 3 none · inbound leads 15 none · 0 stuck"); got != "Sync failures · outbound 12 · inbound 15 · 0 stuck" {
		t.Fatalf("reason %q", got)
	}

	if got, _ := rookSummary(rookSample(at)); got != "1 need you · 2 working" {
		t.Fatalf("summary %q", got)
	}
	if rookAgo(at, at.Add(-90*time.Minute).UnixMilli()) != "1h" || rookAgo(at, at.Add(-20*time.Second).UnixMilli()) != "now" {
		t.Fatal("rookAgo")
	}
	if rookClock(at, at.Add(-64*time.Minute).UnixMilli()) != "1:04" {
		t.Fatal("rookClock")
	}
	_ = image.Point{}
}
