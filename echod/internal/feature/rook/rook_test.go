package rook

import (
	"strings"
	"testing"
)

func TestReadEvents(t *testing.T) {
	in := "id: 1\nevent: sessions\ndata: {\"a\":1}\n\n: comment\nevent: ping\ndata: {}\n\nid: 3\ndata: one\ndata: two\n\n"
	var got []string
	if err := readEvents(strings.NewReader(in), func(e string, d []byte) { got = append(got, e+"="+string(d)) }); err != nil {
		t.Fatal(err)
	}
	want := []string{`sessions={"a":1}`, `ping={}`, "=one\ntwo"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAndOrder(t *testing.T) {
	b := &bridge{url: "http://x", connected: true}
	mu.Lock()
	was := bridges
	bridges = []*bridge{b}
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); bridges = was; mu.Unlock() })

	snap := `{"machine":"mac-mini","sessions":[
		{"id":"1","name":"idle-old","status":"idle","statusSince":100},
		{"id":"2","name":"busy","status":"busy","statusSince":300},
		{"id":"3","name":"asks","status":"waiting","waitingFor":"approve Bash","statusSince":500},
		{"id":"4","name":"busy-old","status":"busy","statusSince":200}],
		"usage":{"fiveHour":{"usedPercent":42,"resetsAt":9},"weekly":{"usedPercent":7}},
		"production":{"reachable":true,"status":"ok","alerts":[]}}`
	if !apply(b, "snapshot", []byte(snap)) {
		t.Fatal("snapshot not applied")
	}
	v := Snapshot()
	var order []string
	for _, s := range v.Sessions {
		order = append(order, s.Name)
		if s.Machine != "mac-mini" {
			t.Fatalf("machine %q", s.Machine)
		}
	}
	if strings.Join(order, ",") != "asks,busy-old,busy,idle-old" {
		t.Fatalf("order %v", order)
	}
	if v.Waiting() != 1 || v.Busy() != 2 || v.Connected != 1 {
		t.Fatalf("counts %d %d %d", v.Waiting(), v.Busy(), v.Connected)
	}
	if v.Usage == nil || v.Usage.FiveHour.UsedPercent != 42 || v.Production == nil || v.Production.Status != "ok" {
		t.Fatalf("usage/production %+v %+v", v.Usage, v.Production)
	}
	if !apply(b, "sessions", []byte(`{"machine":"mac-mini","sessions":[]}`)) || len(Snapshot().Sessions) != 0 {
		t.Fatal("sessions event did not replace the list")
	}
	if apply(b, "ping", []byte(`{}`)) {
		t.Fatal("a ping changed something")
	}
}
