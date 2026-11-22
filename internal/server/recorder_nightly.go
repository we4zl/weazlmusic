package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const nightlyPrefix = "nightly-pod-"

func nightlyID(id string) bool { return strings.HasPrefix(id, nightlyPrefix) }

// An operator selects an existing job. Its actual station snapshots and owner
// identify the account, even when another account has similarly named presets.
func (a *apiV1) configureNightly(source string) error {
	id := nightlyPrefix + digest(source)[:24]
	if err := a.get("schedule", id, "*", &a.nightly); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var seed recordingJob
	if err := a.get("job", source, "*", &seed); err != nil {
		return fmt.Errorf("nightly source: %w", err)
	}
	if seed.Tombstone || len(seed.Stations) == 0 || len(seed.Stations) > 6 {
		return errors.New("invalid nightly source station set")
	}
	owner := accountID(seed.Username)
	rows, err := a.db.Query("SELECT id,body FROM records WHERE kind='stations' AND owner=?", owner)
	if err != nil {
		return err
	}
	var key string
	for rows.Next() {
		var id string
		var body []byte
		var c stationCollection
		if err = rows.Scan(&id, &body); err != nil {
			break
		}
		if err = a.open(body, &c); err != nil {
			break
		}
		matches := 0
		for _, s := range c.Stations {
			for _, p := range seed.Stations {
				if s.ID == p.StationID && s.URL == p.Station.URL {
					matches++
				}
			}
		}
		if matches == len(seed.Stations) {
			key = id
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if key == "" {
		return errors.New("nightly source stations no longer match their library")
	}
	in := scheduleInput{Name: "Overnight Flight Recorder", Zone: "America/New_York", Rule: recurrence{Kind: "daily", LocalStart: "00:30", LocalEnd: "05:00"}}
	var snaps []nativeStation
	for _, p := range seed.Stations {
		snaps = append(snaps, p.Station)
		in.StationIDs = append(in.StationIDs, p.StationID)
	}
	start, end, corrections, err := scheduleWindow(in, a.now())
	if err != nil {
		return err
	}
	in.Starts, in.Ends = start, end
	s := recordingSchedule{scheduleInput: in, ID: id, Version: 1, State: "active", Username: seed.Username, Library: seed.Library, StateKey: key, NextStarts: start, NextEnds: end, Snapshots: snaps, Corrections: corrections}
	j := a.makeJob(id+"-"+itoa(start.Unix()), id, in, &session{User: s.Username}, snaps)
	j.Library = s.Library
	if err = a.saveScheduleJob(s, j); err != nil {
		return err
	}
	a.nightly = s
	log.Printf("Nightly recorder scheduled %s to %s with %d stations", start, end, len(snaps))
	return nil
}

func explicitOverlap(automatic recordingJob, rows []json.RawMessage) bool {
	for _, raw := range rows {
		var j recordingJob
		if json.Unmarshal(raw, &j) != nil || nightlyID(j.ScheduleID) || j.Tombstone || j.State == "cancelled" || j.State == "failed" || j.State == "missed" {
			continue
		}
		if j.Username == automatic.Username && j.Library == automatic.Library && j.Starts.Before(automatic.Ends) && automatic.Starts.Before(j.Ends) {
			return true
		}
	}
	return false
}
func (a *apiV1) skipNightly(j recordingJob, why string) {
	j.State = "cancelled"
	j.StopReason = why
	j.Version++
	if err := a.put("job", j.ID, accountID(j.Username), j); err != nil {
		log.Printf("skip overnight recording: %v", err)
	}
}
func (a *apiV1) nightlyFits(j recordingJob) bool {
	v, e := a.storage()
	u, r, err := a.accountStorage(j.Username)
	return e == nil && err == nil && estimateBytes(j.Ends.Sub(a.now()), j.StationCount) <= min(v.Available, a.accountBudget()-u-r)
}

func (a *apiV1) serviceNightly(rows []json.RawMessage) {
	if a.nightly.ID == "" {
		return
	}
	sweep := !a.now().Before(a.nextNightlySweep)
	if !sweep {
		activeNightly := false
		for id := range a.active {
			if strings.HasPrefix(id, a.nightly.ID+"-") {
				activeNightly = true
				break
			}
		}
		if !activeNightly {
			return
		}
	}
	var newest recordingJob
	var old []recordingJob
	for _, raw := range rows {
		var j recordingJob
		if json.Unmarshal(raw, &j) != nil || j.Username != a.nightly.Username || j.Library != a.nightly.Library || j.Tombstone {
			continue
		}
		auto := j.ScheduleID == a.nightly.ID
		if auto && j.State == "recording" && a.active[j.ID] != nil && j.StopReason == "" && explicitOverlap(j, rows) {
			j.StopReason = "explicit_override"
			j.State = "finalizing"
			j.Version++
			if err := a.put("job", j.ID, accountID(j.Username), j); err == nil {
				a.active[j.ID]()
			}
		}
		if auto && finalState(j.State) {
			old = append(old, j)
		}
		if j.ManifestRevision != "" && j.Bytes > 0 && (j.State == "complete" || j.State == "partial") && j.StopReason != "explicit_override" && (newest.ID == "" || j.Starts.After(newest.Starts)) {
			newest = j
		}
	}
	if !sweep {
		return
	}
	a.nextNightlySweep = a.now().Add(30 * time.Second)
	if newest.ID == "" {
		_ = os.Remove(filepath.Join(a.s.cfg.DataDir, "recordings", "overnight-pod.json"))
		return
	}
	var manifest recordingManifest
	if err := a.get("manifest", newest.ID, accountID(newest.Username), &manifest); err != nil || manifest.Revision != newest.ManifestRevision || manifest.Bytes == 0 {
		return
	}
	// Private read-only handoff for the owner's SSH-connected dock helper.
	path := filepath.Join(a.s.cfg.DataDir, "recordings", "overnight-pod.json")
	data, err := json.Marshal(struct {
		recordingManifest
		Account string `json:"accountId"`
		Library string `json:"libraryId"`
	}{manifest, accountID(newest.Username), newest.Library})
	if err != nil {
		return
	}
	previous, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(previous, data) {
		checked := map[string]bool{}
		for _, track := range manifest.Tracks {
			for _, seg := range track.Segments {
				if checked[seg.AssetID] {
					continue
				}
				if seg.AssetID == "" || filepath.Base(seg.AssetID) != seg.AssetID {
					return
				}
				info, e := os.Stat(filepath.Join(filepath.Dir(path), seg.AssetID+".m4a"))
				if e != nil || !info.Mode().IsRegular() || info.Size() != seg.Bytes {
					return
				}
				checked[seg.AssetID] = true
			}
		}
		if len(checked) == 0 {
			return
		}
		if err = writeDurable(path+".tmp", data); err != nil {
			return
		}
		if err = os.Rename(path+".tmp", path); err != nil {
			return
		}
		if err = syncDir(filepath.Dir(path)); err != nil {
			return
		}
	}
	for _, j := range old {
		if j.ID != newest.ID && j.Starts.Before(newest.Starts) {
			if err := a.purgeNightly(j); err != nil {
				log.Printf("overnight cleanup: %v", err)
			}
		}
	}
}

// Delete the manifest and media handles atomically. The existing durable
// garbage queue unlinks bytes and retries unfinished disk cleanup on restart.
func (a *apiV1) purgeNightly(j recordingJob) error {
	if !nightlyID(j.ScheduleID) || !finalState(j.State) {
		return errors.New("expected a finalized automatic recording")
	}
	owner := accountID(j.Username)
	g := recordingGarbage{ID: "purge-" + j.ID, Owner: owner}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, p := range j.Stations {
		for _, seg := range p.Segments {
			if seen[seg.AssetID] {
				continue
			}
			seen[seg.AssetID] = true
			if filepath.Base(seg.AssetID) != seg.AssetID || seg.AssetID == "" {
				return errors.New("invalid recording asset")
			}
			g.Paths = append(g.Paths, seg.AssetID+".m4a")
			if _, err = tx.Exec("DELETE FROM records WHERE kind='asset' AND id=? AND owner=?", seg.AssetID, owner); err != nil {
				return err
			}
		}
	}
	if len(g.Paths) > 0 {
		b, e := a.seal(g)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO records VALUES('recording-garbage',?,?,?)", g.ID, owner, b); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM records WHERE kind IN ('job','manifest') AND id=? AND owner=?", j.ID, owner); err != nil {
		return err
	}
	return tx.Commit()
}
