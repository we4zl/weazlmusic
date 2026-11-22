package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOvernightRollover(t *testing.T) {
	h := setup(t)
	a := h.app.Config.Handler.(*Server).v1
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	se := &session{User: "alice"}
	owner := accountID(se.User)
	stations := []nativeStation{{ID: "one", Name: "One", URL: "https://radio.example/one", Preset: true}}
	seed := a.makeJob("seed", "", scheduleInput{Name: "explicit", Starts: now.Add(-96 * time.Hour), Ends: now.Add(-92 * time.Hour)}, se, stations)
	if err := a.put("stations", "station-set", owner, stationCollection{Stations: stations}); err != nil {
		t.Fatal(err)
	}
	if err := a.put("job", seed.ID, owner, seed); err != nil {
		t.Fatal(err)
	}
	if err := a.configureNightly(seed.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.configureNightly(seed.ID); err != nil {
		t.Fatal(err)
	}
	s := a.nightly
	loc, _ := time.LoadLocation("America/New_York")
	if s.NextStarts.In(loc).Format("15:04") != "00:30" || s.NextEnds.In(loc).Format("15:04") != "05:00" || len(s.Snapshots) != 1 {
		t.Fatal(s)
	}
	if !a.capacity(scheduleInput{Starts: s.NextStarts, Ends: s.NextEnds}, 6, "") {
		t.Fatal("nightly blocked an explicit reservation")
	}
	makeDone := func(id, schedule string, age time.Duration) recordingJob {
		j := a.makeJob(id, schedule, scheduleInput{Name: id, Starts: now.Add(-age), Ends: now.Add(-age + time.Hour)}, se, stations)
		asset := digest(id)[:40]
		data := []byte{1, 2, 3}
		if err := os.WriteFile(filepath.Join(a.s.cfg.DataDir, "recordings", asset+".m4a"), data, 0600); err != nil {
			t.Fatal(err)
		}
		j.Stations[0].Segments = []recordingSegment{{ID: asset, AssetID: asset, Duration: 1000, Bytes: 3, Hash: digest("abc")}}
		if err := a.put("asset", asset, owner, assetRecord{ID: asset, Session: j.ID, Path: asset + ".m4a", Bytes: 3}); err != nil {
			t.Fatal(err)
		}
		if err := a.put("job", id, owner, j); err != nil {
			t.Fatal(err)
		}
		a.finalize(&j)
		return j
	}
	explicit := makeDone("manual", "", 80*time.Hour)
	first := makeDone("night1", s.ID, 72*time.Hour)
	sweep := func() {
		rows, err := a.list("job", "*")
		if err != nil {
			t.Fatal(err)
		}
		a.nextNightlySweep = time.Time{}
		a.serviceNightly(rows)
		a.cleanupRecordingGarbage(999)
	}
	sweep()
	var got recordingJob
	if a.get("job", first.ID, owner, &got) != nil {
		t.Fatal("last playable was expired after 24 hours")
	}
	fail := a.makeJob("failed-night", s.ID, scheduleInput{Starts: now.Add(-48 * time.Hour), Ends: now.Add(-44 * time.Hour)}, se, stations)
	if err := a.put("job", fail.ID, owner, fail); err != nil {
		t.Fatal(err)
	}
	a.finalize(&fail)
	sweep()
	manifest := func() recordingManifest {
		var m recordingManifest
		b, e := os.ReadFile(filepath.Join(a.s.cfg.DataDir, "recordings", "overnight-pod.json"))
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(b, &m); e != nil {
			t.Fatal(e)
		}
		return m
	}
	if manifest().Session != first.ID {
		t.Fatal("failed night replaced the dial")
	}
	override := makeDone("explicit-replacement", "", 24*time.Hour)
	sweep()
	if manifest().Session != override.ID {
		t.Fatal("explicit recording unavailable for sync")
	}
	if a.get("job", first.ID, owner, &got) == nil {
		t.Fatal("old auto still exists")
	}
	for _, j := range []recordingJob{explicit, override} {
		if a.get("job", j.ID, owner, &got) != nil {
			t.Fatal("removed explicit session")
		}
	}
	if _, e := os.Stat(filepath.Join(a.s.cfg.DataDir, "recordings", first.Stations[0].Segments[0].AssetID+".m4a")); !os.IsNotExist(e) {
		t.Fatal("old bytes survived", e)
	}
	// A manual request arriving during capture preempts only the automatic job.
	active := a.makeJob(s.ID+"-active", s.ID, scheduleInput{Starts: now, Ends: now.Add(time.Hour)}, se, stations)
	active.State = "recording"
	manual := a.makeJob("manual-now", "", scheduleInput{Starts: now, Ends: active.Ends}, se, stations)
	_ = a.put("job", active.ID, owner, active)
	_ = a.put("job", manual.ID, owner, manual)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.active[active.ID] = cancel
	defer delete(a.active, active.ID)
	sweep()
	if ctx.Err() == nil || a.get("job", active.ID, owner, &got) != nil || got.StopReason != "explicit_override" {
		t.Fatal("manual did not preempt")
	}
}
