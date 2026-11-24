package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	first := "321abcde-0000-4000-8000-000000000013"
	second := "321abcde-0000-4000-8000-000000000014"
	headers := map[string]string{"X-Weazl-Device": device}
	change := func(token, action, writer string, gen uint64, want int) {
		t.Helper()
		code, body, _ := h.vrequest("PUT", "portable/source", podWriterChange{action, writer, &gen}, token, headers)
		if code != want {
			t.Fatalf("%s: %d %s", action, code, body)
		}
	}
	save := func(token, writer, entry, next string, gen, seq uint64, want int) {
		t.Helper()
		q := map[string]any{"writer": writer, "generation": gen, "sequence": seq,
			"current": map[string]any{"id": "0-0", "_entryId": entry, "title": "Repeat", "position": 7.5},
			"queue":   []map[string]any{{"id": "0-0", "_entryId": next, "title": "Repeat"}}}
		code, body, _ := h.vrequest("PUT", "portable/checkpoint", q, token, headers)
		if code != want {
			t.Fatalf("checkpoint %s: %d %s", entry, code, body)
		}
	}
	current := func(entry, next string) {
		t.Helper()
		w := httptest.NewRecorder()
		a.dockRequest(w, httptest.NewRequest("GET", "/queue", nil))
		got := decodeData[struct {
			Source  string
			Current struct {
				ID       string
				Entry    string `json:"_entryId"`
				Position float64
			}
			Queue []struct {
				ID    string
				Entry string `json:"_entryId"`
			}
		}](t, w.Body.Bytes())
		if got.Source != device || got.Current.ID != "0-0" || got.Current.Entry != entry ||
			got.Current.Position != 7.5 || len(got.Queue) != 1 || got.Queue[0].ID != "0-0" || got.Queue[0].Entry != next {
			t.Fatal(got)
		}
	}
	change(bob.Access, "follow", first, 0, 403)
	// Upgraded installs retain the last checkpoint without granting ownership to
	// the page that merely restored it. The old writer-less PUT is fenced off.
	session, err := a.deviceIdentity(deviceSession{Username: "alice", AccessExpires: a.now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	legacy := userState{
		Current: json.RawMessage(`{"id":"0-0","_entryId":"legacy","position":7.5}`),
		Queue:   []json.RawMessage{json.RawMessage(`{"id":"0-0","_entryId":"next"}`)},
	}
	owner, key := accountID(session.User), session.stateKey()
	if err := a.put("pod-source", key, owner, device); err != nil {
		t.Fatal(err)
	}
	if err := a.put("pod-checkpoint", key+":browser:"+device, owner, legacy); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.vrequest("PUT", "portable/checkpoint", legacy, alice.Access, headers); code != 422 {
		t.Fatal(code)
	}
	current("legacy", "next")
	change(alice.Access, "follow", first, 0, 200)
	current("legacy", "next")
	save(bob.Access, first, "first", "second", 1, 1, 403)
	save(alice.Access, first, "first", "second", 1, 2, 204)
	save(alice.Access, first, "older", "first", 1, 1, 409)
	save(alice.Access, second, "older", "first", 1, 3, 409)
	current("first", "second")
	change(alice.Access, "claim", second, 1, 200)
	current("first", "second")
	save(alice.Access, first, "first", "second", 1, 3, 409)
	save(alice.Access, second, "second", "third", 2, 1, 204)
	change(alice.Access, "claim", first, 1, 409)
	current("second", "third")
	change(alice.Access, "stop", second, 2, 200)
	save(alice.Access, second, "third", "fourth", 2, 2, 409)
	change(alice.Access, "claim", second, 3, 409)
	current("second", "third")
	change(alice.Access, "follow", first, 3, 200)
	save(alice.Access, first, "third", "fourth", 4, 1, 204)
	current("third", "fourth")
	w := httptest.NewRecorder()
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

func TestSafeEjectSupersedesInterruptedCopy(t *testing.T) {
	h := setup(t)
	h.login("alice")
	alice := h.native("alice", "dock")
	a := h.app.Config.Handler.(*Server).v1
	a.nightly = recordingSchedule{ID: "pod", Username: "alice", Library: alice.Library}
	for _, phase := range []string{"ready", "shelves"} {
		if code, body, _ := h.vrequest("PUT", "portable/status", packReport{Phase: phase}, alice.Access, nil); code != 204 {
			t.Fatal(code, string(body))
		}
	}
	dir := filepath.Join(h.dir, "recordings")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(dockReport{Phase: "ejected", Seen: a.now().Add(time.Second)})
	if err := os.WriteFile(filepath.Join(dir, "pod-dock.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	code, body, _ := h.vrequest("GET", "portable/status", nil, alice.Access, nil)
	if code != 200 {
		t.Fatal(code, string(body))
	}
	out := decodeData[struct {
		Music packHistory
		Dock  dockReport
	}](t, body)
	if out.Dock.Phase != "ejected" || out.Music.Latest.Phase != "ejected" || out.Music.Ready.Phase != "ready" {
		t.Fatal(string(body))
	}
}
