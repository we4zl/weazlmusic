package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The USB companion submits a small receipt. The card and its media stay local.
type packReport struct {
	QueueKey   string          `json:"queueKey,omitempty"`
	Message    string          `json:"message,omitempty"`
	Phase      string          `json:"phase"`
	Title      string          `json:"title"`
	Done       int64           `json:"done"`
	Total      int64           `json:"total"`
	Position   int64           `json:"positionMs"`
	Queue      int             `json:"queueTracks"`
	Free       int64           `json:"freeBytes"`
	Seen       time.Time       `json:"observedAt"`
	Active     []string        `json:"active,omitempty"`
	Selections []packSelection `json:"selections,omitempty"`
}

type packSelection struct {
	Name   string `json:"name"`
	Tracks int    `json:"tracks"`
}

func (m packReport) validDetails() bool {
	if len(m.Active) > 5 || len(m.Selections) > 32 {
		return false
	}
	for _, title := range m.Active {
		if len(title) == 0 || len(title) > 1024 {
			return false
		}
	}
	for _, selection := range m.Selections {
		if len(selection.Name) == 0 || len(selection.Name) > 1024 || selection.Tracks <= 0 {
			return false
		}
	}
	return true
}

type packHistory struct {
	Latest *packReport `json:"latest"`
	Ready  *packReport `json:"ready"`
}

type dockReport struct {
	Hash     string     `json:"hash,omitempty"`
	Verified *time.Time `json:"verifiedAt,omitempty"`
	Account  string     `json:"accountId,omitempty"`
	Library  string     `json:"libraryId,omitempty"`
	Phase    string     `json:"phase"`
	Name     string     `json:"name,omitempty"`
	Done     int64      `json:"done"`
	Total    int64      `json:"total"`
	Seen     time.Time  `json:"observedAt"`
}

func readPodFile(dir, name string, target any) error {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(target)
}

func (a *apiV1) portableStatus(w http.ResponseWriter, r *http.Request, se *session) {
	owner, library := accountID(se.User), a.libraryID(se)
	key := se.podKey(digest(owner + "/" + library))
	var music packHistory
	if err := a.get("pod-pack", key, owner, &music); err != nil && !errors.Is(err, sql.ErrNoRows) {
		verror(w, 503, "storage_unavailable", "Could not read the last music pack.")
		return
	}
	if r.Method == "PUT" {
		var m packReport
		if err := decode(r, &m); err != nil || library == "" || len(m.QueueKey) > 16 || len(m.Message) > 320 || len(m.Title) > 1024 || m.Done < 0 || m.Done > m.Total || m.Total > 1<<50 || m.Position < 0 || m.Queue < 0 || m.Free < 0 || !m.validDetails() {
			verror(w, 422, "validation_failed", "Invalid music pack receipt.")
			return
		}
		switch m.Phase {
		case "preparing", "radio", "music", "shelves", "indexing", "ready", "failed":
		default:
			verror(w, 422, "validation_failed", "Unknown pack phase.")
			return
		}
		m.Seen = a.now().UTC()
		music.Latest = &m
		if m.Phase == "ready" {
			music.Ready = &m
		}
		if err := a.put("pod-pack", key, owner, music); err != nil {
			verror(w, 503, "storage_unavailable", "Could not save the pack receipt.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	devices, err := a.userPods(se)
	if err != nil {
		verror(w, 503, "storage", "Cannot list players.")
		return
	}
	out := map[string]any{"music": music, "devices": devices}
	// The workstation reports on the operator's one configured radio dock. Only
	// the owner of that overnight schedule sees its connection and station data.
	if a.podPaired(se) {
		listening, err := a.podListening(se)
		if err != nil {
			verror(w, 503, "storage_unavailable", "Could not read the dock source.")
			return
		}
		for key, value := range listening.writerStatus(r.Header.Get("X-Weazl-Device"), r.Header.Get("X-Weazl-Writer")) {
			out[key] = value
		}
		out["paired"] = true
		if se.PodID != "" {
			for field, kind := range map[string]string{"dock": "pod-radio", "lastRadio": "pod-radio-ready"} {
				var report dockReport
				err := a.get(kind, se.PodID, owner, &report)
				if err == nil {
					out[field] = report
				} else if !errors.Is(err, sql.ErrNoRows) {
					verror(w, 503, "storage", "Cannot read recorder receipt.")
					return
				}
			}
		}
		if se.PodID != "" {
			v, err := a.verification(se)
			if err != nil {
				verror(w, 503, "storage", "Cannot check verification.")
				return
			}
			_, capable := a.podArt.Load(se.PodID)
			out["supportsCardVerification"] = capable || v.ID != ""
			out["verification"] = v
			out["musicMatch"] = v.State != "queued" && v.State != "failed" && a.receiptStatus(se, music.Latest)
		}
		if !a.legacyPodPaired(se) {
			dataOut(w, 200, out)
			return
		}
		var schedule recordingSchedule
		if err := a.get("schedule", a.nightly.ID, owner, &schedule); err == nil && schedule.State == "active" {
			out["schedule"] = map[string]any{"startsAt": schedule.NextStarts, "endsAt": schedule.NextEnds, "zone": schedule.Zone, "stations": len(schedule.Snapshots)}
		}
		dir := filepath.Join(a.s.cfg.DataDir, "recordings")
		var edition struct {
			SessionID, Revision string
			Account             string    `json:"accountId"`
			Library             string    `json:"libraryId"`
			Name                string    `json:"name"`
			Starts              time.Time `json:"startsAt"`
			Tracks              []struct {
				Name string `json:"name"`
			} `json:"tracks"`
		}
		if err := readPodFile(dir, "overnight-pod.json", &edition); err == nil && edition.Account == owner && edition.Library == library {
			out["edition"] = map[string]any{"hash": cardHash(digest(edition.SessionID + ":" + edition.Revision)), "name": edition.Name, "startsAt": edition.Starts, "stations": len(edition.Tracks)}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			verror(w, 503, "storage_unavailable", "Could not read the published radio edition.")
			return
		}
		if se.PodID != "" {
			dataOut(w, 200, out)
			return
		}
		for field, file := range map[string]string{"dock": "pod-dock.json", "lastRadio": "pod-dock-ready.json"} {
			var d dockReport
			if err := readPodFile(dir, file, &d); err == nil && ((field == "dock" && d.Account == "" && d.Library == "" && (d.Phase == "h2-control" || d.Phase == "waiting" || d.Phase == "ejected" || d.Phase == "checking" || d.Phase == "no-edition" || d.Phase == "failed")) || (d.Account == owner && d.Library == library)) {
				out[field] = d
				if field == "dock" && (d.Phase == "ejected" || d.Phase == "waiting") &&
					music.Latest != nil && !music.Latest.Seen.After(d.Seen) && music.Latest.Phase != "ready" {
					music.Latest = &packReport{Phase: d.Phase, Seen: d.Seen}
					out["music"] = music
				}
			}
		}
	}
	dataOut(w, 200, out)
}
