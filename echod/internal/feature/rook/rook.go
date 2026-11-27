// Package rook follows Rook bridges (Method-Platform/android-status-monitor's bridge/): each one
// reports the Claude Code sessions on its machine, its usage windows and production's status over a
// server-sent event stream at /events. The display's Rook screen (feature/display/rook_screen.go) draws
// what this package holds; nothing else in the daemon knows it.
//
// It is configured by a file of its own on userdata, so an update leaves it in place and the upstream
// config is untouched: /data/techo5-linux/rook.json, or the file ROOK_CONFIG names:
//
//	{"bridges": ["http://192.168.1.183:8787", "http://192.168.1.71:8787"]}
//
// With no file, or no bridges in it, the screen is not offered and nothing here runs.
package rook

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultConfig is where the bridges are listed on a device.
const DefaultConfig = "/data/techo5-linux/rook.json"

// Session is one Claude Code session a bridge reports.
type Session struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Project     string  `json:"project"`
	Status      string  `json:"status"` // busy, idle or waiting
	WaitingFor  *string `json:"waitingFor"`
	Title       *string `json:"title"`
	Kind        string  `json:"kind"`
	StatusSince int64   `json:"statusSince"` // ms since the epoch
	StartedAt   int64   `json:"startedAt"`
	Machine     string  `json:"-"`
}

// Label is what a session is called on the screen: its own title, else its name, else its project.
func (s Session) Label() string {
	if s.Title != nil && strings.TrimSpace(*s.Title) != "" {
		return *s.Title
	}
	if s.Name != "" {
		return s.Name
	}
	return s.Project
}

// Codex is the Codex subscription window, as of the last Codex session on the machine that reported it.
type Codex struct {
	Primary *struct {
		UsedPercent int `json:"usedPercent"`
	} `json:"primary"`
	ObservedAt int64  `json:"observedAt"`
	Authority  bool   `json:"authority"`
	Machine    string `json:"machine"`
}

// CI is the release branches' CI across the repos a bridge watches.
type CI struct {
	Running int `json:"runningCount"`
	Broken  int `json:"brokenCount"`
}

// Window is one usage window: the five-hour one or the week.
type Window struct {
	UsedPercent int   `json:"usedPercent"`
	ResetsAt    int64 `json:"resetsAt"`
}

// Usage is a machine's usage windows.
type Usage struct {
	FiveHour *Window `json:"fiveHour"`
	Weekly   *Window `json:"weekly"`
}

// Production is the one bridge that polls production's report of it.
type Production struct {
	Reachable bool     `json:"reachable"`
	Status    string   `json:"status"`
	Alerts    []string `json:"alerts"`
	Version   string   `json:"version"`
	Detail    *Detail  `json:"detail"` // nil until the bridge has a health snapshot
}

// Detail is production's health snapshot: what the Health page draws.
type Detail struct {
	Env  string `json:"env"`
	KPIs []KPI  `json:"kpis"`
	// Components are every part of production, grouped application, data, workers, edge.
	Components []Component `json:"components"`
	Queues     struct {
		Total          int `json:"total"`
		Unhealthy      int `json:"unhealthy"`
		Waiting        int `json:"waiting"`
		Delayed        int `json:"delayed"`
		FailedLastHour int `json:"failedLastHour"`
	} `json:"queues"`
	Mongo *struct {
		Members   int  `json:"members"`
		PrimaryOK bool `json:"primaryOk"`
		MaxLagSec *int `json:"maxLagSec"`
	} `json:"mongo"`
	Redis *struct {
		UsedMB    int `json:"usedMb"`
		MaxMB     int `json:"maxMb"`
		Evicted   int `json:"evictedLastHour"`
		OpsPerSec int `json:"opsPerSec"`
	} `json:"redis"`
	Solr *struct {
		Up        bool  `json:"up"`
		Cores     int   `json:"cores"`
		Unhealthy int   `json:"unhealthyCores"`
		Documents int64 `json:"documents"`
	} `json:"solr"`
	Errors struct {
		Count int `json:"count"`
	} `json:"errors"`
	Integrations struct {
		Total     int `json:"total"`
		Unhealthy int `json:"unhealthy"`
	} `json:"integrations"`
	Releases []struct {
		App     string `json:"app"`
		Release string `json:"release"`
		AgoSec  int64  `json:"deployedAgoSec"`
	} `json:"releases"`
}

// KPI is one of production's headline numbers with its last hour, a point a minute; a nil point is a
// gap in the series, not a zero.
type KPI struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Value  string     `json:"value"`
	Delta  *string    `json:"deltaText"`
	Tone   *string    `json:"tone"` // good, bad, neutral or nil
	Unit   *string    `json:"unit"`
	Points []*float64 `json:"points"`
}

// Component is one part of production and how it is.
type Component struct {
	Name   string  `json:"name"`
	Group  string  `json:"group"`
	Status string  `json:"status"` // healthy, degraded, down, unknown
	Reason *string `json:"reason"`
}

// View is everything the bridges report, merged: what a frame of the screen shows.
type View struct {
	Sessions   []Session   // those needing someone first, then working, then idle; oldest first in each
	Usage      *Usage      // the first bridge's that reported one
	Production *Production // the bridge that polls it, if any does
	Codex      *Codex      // the authority's, else the freshest
	CI         *CI         // the bridge that watches it, if any does
	Bridges    int         // configured
	Connected  int         // with a live stream now
}

// Waiting is how many sessions are waiting for someone.
func (v View) Waiting() int { return v.count("waiting") }

// Busy is how many sessions are working.
func (v View) Busy() int { return v.count("busy") }

func (v View) count(status string) int {
	n := 0
	for _, s := range v.Sessions {
		if s.Status == status {
			n++
		}
	}
	return n
}

type config struct {
	Bridges []string `json:"bridges"`
}

func configPath() string {
	if p := os.Getenv("ROOK_CONFIG"); p != "" {
		return p
	}
	return DefaultConfig
}

var (
	cfgOnce sync.Once
	cfg     config
)

func loadConfig() config {
	cfgOnce.Do(func() {
		b, err := os.ReadFile(configPath())
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("rook: config unreadable", "path", configPath(), "err", err)
			}
			return
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			slog.Warn("rook: config unreadable", "path", configPath(), "err", err)
		}
		cfg.Bridges = slices.DeleteFunc(cfg.Bridges, func(s string) bool { return strings.TrimSpace(s) == "" })
		slog.Info("rook: bridges", "path", configPath(), "bridges", len(cfg.Bridges))
	})
	return cfg
}

// Configured is whether any bridge is listed: the screen is offered only then.
func Configured() bool { return len(loadConfig().Bridges) > 0 }

// bridge is what one bridge last said.
type bridge struct {
	url        string
	machine    string
	connected  bool
	sessions   []Session
	usage      *Usage
	production *Production
	codex      *Codex
	ci         *CI
}

var (
	mu      sync.Mutex
	bridges []*bridge
	started sync.Once
	notify  func()
)

// Start begins following every configured bridge, once; changed is called whenever what they report
// changes (from the streams' goroutines).
func Start(changed func()) {
	started.Do(func() {
		mu.Lock()
		notify = changed
		for _, u := range loadConfig().Bridges {
			b := &bridge{url: strings.TrimRight(u, "/")}
			bridges = append(bridges, b)
			go follow(context.Background(), b)
		}
		mu.Unlock()
	})
}

func changedNow() {
	mu.Lock()
	n := notify
	mu.Unlock()
	if n != nil {
		n()
	}
}

// follow keeps one bridge's stream open, reconnecting with a backoff up to 30 seconds.
func follow(ctx context.Context, b *bridge) {
	wait := time.Second
	for ctx.Err() == nil {
		began := time.Now()
		err := stream(ctx, b)
		mu.Lock()
		b.connected = false
		mu.Unlock()
		changedNow()
		if time.Since(began) > time.Minute {
			wait = time.Second
		}
		slog.Info("rook: bridge stream ended", "bridge", b.url, "err", err, "retry", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

// client has no overall timeout: the stream is meant to stay open. The bridge pings every few
// seconds, so a dead one is noticed by the read deadline in stream.
var client = &http.Client{}

func stream(ctx context.Context, b *bridge) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// A stream silent for a minute (the bridge pings far more often) is dead: cancel it.
	alive := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-alive:
				t.Reset(time.Minute)
			case <-t.C:
				cancel()
				return
			}
		}
	}()

	mu.Lock()
	b.connected = true
	mu.Unlock()
	changedNow()
	return readEvents(resp.Body, func(event string, data []byte) {
		select {
		case alive <- struct{}{}:
		default:
		}
		if apply(b, event, data) {
			changedNow()
		}
	})
}

// readEvents splits a server-sent event stream into its events.
func readEvents(r interface{ Read([]byte) (int, error) }, on func(event string, data []byte)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024) // a snapshot with production's detail is large
	var event string
	var data []byte
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if event != "" || len(data) > 0 {
				on(event, data)
			}
			event, data = "", nil
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, strings.TrimPrefix(line[len("data:"):], " ")...)
		}
	}
	return sc.Err()
}

// apply takes one event into the bridge's state, reporting whether anything the screen shows changed.
func apply(b *bridge, event string, data []byte) bool {
	switch event {
	case "snapshot":
		var s struct {
			Machine    string      `json:"machine"`
			Sessions   []Session   `json:"sessions"`
			Usage      *Usage      `json:"usage"`
			Production *Production `json:"production"`
			Codex      *Codex      `json:"codex"`
			CI         *CI         `json:"ci"`
		}
		if json.Unmarshal(data, &s) != nil {
			return false
		}
		mu.Lock()
		b.machine, b.sessions, b.usage, b.production = s.Machine, tag(s.Sessions, s.Machine), s.Usage, s.Production
		b.codex, b.ci = s.Codex, s.CI
		mu.Unlock()
		return true
	case "sessions":
		var s struct {
			Machine  string    `json:"machine"`
			Sessions []Session `json:"sessions"`
		}
		if json.Unmarshal(data, &s) != nil {
			return false
		}
		mu.Lock()
		b.machine, b.sessions = s.Machine, tag(s.Sessions, s.Machine)
		mu.Unlock()
		return true
	case "usage":
		var u Usage
		if json.Unmarshal(data, &u) != nil {
			return false
		}
		mu.Lock()
		b.usage = &u
		mu.Unlock()
		return true
	case "production":
		var p Production
		if json.Unmarshal(data, &p) != nil {
			return false
		}
		mu.Lock()
		b.production = &p
		mu.Unlock()
		return true
	case "codex":
		var c Codex
		if json.Unmarshal(data, &c) != nil {
			return false
		}
		mu.Lock()
		b.codex = &c
		mu.Unlock()
		return true
	case "ci":
		var c CI
		if json.Unmarshal(data, &c) != nil {
			return false
		}
		mu.Lock()
		b.ci = &c
		mu.Unlock()
		return true
	}
	return false
}

func tag(ss []Session, machine string) []Session {
	for i := range ss {
		ss[i].Machine = machine
	}
	return ss
}

var rank = map[string]int{"waiting": 0, "busy": 1, "shell": 2, "idle": 3}

// Snapshot is what every bridge last said, merged.
func Snapshot() View {
	mu.Lock()
	defer mu.Unlock()
	v := View{Bridges: len(loadConfig().Bridges)}
	seen := map[string]bool{} // one machine reached at two addresses reports its sessions twice
	machines := map[string]bool{}
	for _, b := range bridges {
		if b.connected {
			if b.machine != "" && machines[b.machine] {
				v.Bridges-- // the same machine again, at another address: count it once
			} else {
				v.Connected++
			}
			machines[b.machine] = true
		}
		for _, s := range b.sessions {
			if k := s.Machine + "/" + s.ID; !seen[k] {
				seen[k] = true
				v.Sessions = append(v.Sessions, s)
			}
		}
		if v.Usage == nil && b.usage != nil && b.usage.FiveHour != nil {
			v.Usage = b.usage
		}
		// A bridge that has gone quiet says nothing about production now: its last word, down or up,
		// would stand on the screen as if it were current.
		if v.Production == nil && b.connected && b.production != nil && b.production.Status != "" {
			v.Production = b.production
		}
		if c := b.codex; c != nil && c.Primary != nil {
			if v.Codex == nil || (c.Authority && !v.Codex.Authority) ||
				(c.Authority == v.Codex.Authority && c.ObservedAt > v.Codex.ObservedAt) {
				v.Codex = c
			}
		}
		if v.CI == nil && b.ci != nil {
			v.CI = b.ci
		}
	}
	sortSessions(v.Sessions)
	return v
}

func sortSessions(ss []Session) {
	slices.SortStableFunc(ss, func(a, b Session) int {
		ra, ok := rank[a.Status]
		if !ok {
			ra = 4
		}
		rb, ok := rank[b.Status]
		if !ok {
			rb = 4
		}
		if ra != rb {
			return ra - rb
		}
		switch {
		case a.StatusSince < b.StatusSince:
			return -1
		case a.StatusSince > b.StatusSince:
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}
