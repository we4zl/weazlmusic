package server

import (
	"encoding/json"
	"net/http/httptest"
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

func TestListeningSourceAndDock(t *testing.T) {
	h := setup(t)
	h.login("alice")
	h.login("bob")
	alice := h.native("alice", "one")
	bob := h.native("bob", "two")
	a := h.app.Config.Handler.(*Server).v1
	a.nightly = recordingSchedule{ID: "pod", Username: "alice", Library: alice.Library}
	device := "321abcde-0000-4000-8000-000000000012"
	headers := map[string]string{"X-Weazl-Device": device}
	if code, _, _ := h.vrequest("PUT", "portable/source", map[string]string{}, bob.Access, headers); code != 403 {
		t.Fatal(code)
	}
	if code, _, _ := h.vrequest("PUT", "portable/source", map[string]string{}, alice.Access, headers); code != 204 {
		t.Fatal(code)
	}
	q := map[string]any{"current": map[string]any{"id": "0-0", "title": "Repeat", "position": 7.5}, "queue": []map[string]any{{"id": "0-0", "title": "Repeat"}}}
	if code, _, _ := h.vrequest("PUT", "portable/checkpoint", q, bob.Access, headers); code != 403 {
		t.Fatal(code)
	}
	if code, _, _ := h.vrequest("PUT", "portable/checkpoint", q, alice.Access, headers); code != 204 {
		t.Fatal(code)
	}
	req := httptest.NewRequest("GET", "/queue", nil)
	w := httptest.NewRecorder()
	a.dockRequest(w, req)
	got := decodeData[struct {
		Source  string
		Current struct{ Position float64 }
		Queue   []json.RawMessage
	}](t, w.Body.Bytes())
	if got.Source != device || len(got.Queue) != 1 || got.Current.Position != 7.5 {
		t.Fatal(got)
	}
	w = httptest.NewRecorder()
	a.dockRequest(w, httptest.NewRequest("GET", "/audio?id=0-0", nil))
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal("dock audio", w.Code)
	}
	w = httptest.NewRecorder()
	a.dockRequest(w, httptest.NewRequest("POST", "/api/v1/auth/login", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
