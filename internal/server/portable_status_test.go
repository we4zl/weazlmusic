package server

import (
	"encoding/json"
	"testing"
)

func TestPortableMusicReceipts(t *testing.T) {
	h := setup(t)
	h.login("alice")
	h.login("bob")
	alice := h.native("alice", "pod-test").Access
	bob := h.native("bob", "pod-test").Access
	for _, phase := range []string{"ready", "failed"} {
		code, body, _ := h.vrequest("PUT", "portable/status", packReport{Phase: phase, Title: "from Alice", Done: 1, Total: 1}, alice, nil)
		if code != 204 {
			t.Fatalf("receipt %s %d %s", phase, code, body)
		}
	}
	read := func(token string) packHistory {
		code, b, _ := h.vrequest("GET", "portable/status", nil, token, nil)
		if code != 200 {
			t.Fatal(code, string(b))
		}
		var e struct{ Data struct{ Music packHistory } }
		if err := json.Unmarshal(b, &e); err != nil {
			t.Fatal(err)
		}
		return e.Data.Music
	}
	m := read(alice)
	if m.Latest.Phase != "failed" || m.Ready.Phase != "ready" || m.Ready.Seen.IsZero() {
		t.Fatal(m)
	}
	if read(bob).Latest != nil {
		t.Fatal("account leak")
	}
}
