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
	Phase    string    `json:"phase"`
	Title    string    `json:"title"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"`
	Position int64     `json:"positionMs"`
	Queue    int       `json:"queueTracks"`
	Free     int64     `json:"freeBytes"`
	Seen     time.Time `json:"observedAt"`
}

type packHistory struct {
	Latest *packReport `json:"latest"`
	Ready  *packReport `json:"ready"`
}

type dockReport struct {
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
	key := digest(owner + "/" + library)
	var music packHistory
	if err := a.get("pod-pack", key, owner, &music); err != nil && !errors.Is(err, sql.ErrNoRows) {
		verror(w, 503, "storage_unavailable", "Could not read the last music pack.")
		return
	}
	if r.Method == "PUT" {
		var m packReport
		if err := decode(r, &m); err != nil || library == "" || len(m.Title) > 1024 || m.Done < 0 || m.Done > m.Total || m.Total > 1<<50 || m.Position < 0 || m.Queue < 0 || m.Free < 0 {
			verror(w, 422, "validation_failed", "Invalid music pack receipt.")
			return
		}
		switch m.Phase {
		case "preparing", "radio", "music", "indexing", "ready", "failed":
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
	out := map[string]any{"music": music}
	// The workstation reports on the operator's one configured radio dock. Only
	// the owner of that overnight schedule sees its connection and station data.
	if a.nightly.ID != "" && a.nightly.Username == se.User && a.nightly.Library == library {
		var schedule recordingSchedule
		if err := a.get("schedule", a.nightly.ID, owner, &schedule); err == nil && schedule.State == "active" {
			out["schedule"] = map[string]any{"startsAt": schedule.NextStarts, "endsAt": schedule.NextEnds, "zone": schedule.Zone, "stations": len(schedule.Snapshots)}
		}
		dir := filepath.Join(a.s.cfg.DataDir, "recordings")
		var edition struct {
			Account string    `json:"accountId"`
			Library string    `json:"libraryId"`
			Name    string    `json:"name"`
			Starts  time.Time `json:"startsAt"`
			Tracks  []struct {
				Name string `json:"name"`
			} `json:"tracks"`
		}
		if err := readPodFile(dir, "overnight-pod.json", &edition); err == nil && edition.Account == owner && edition.Library == library {
			out["edition"] = map[string]any{"name": edition.Name, "startsAt": edition.Starts, "stations": len(edition.Tracks)}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			verror(w, 503, "storage_unavailable", "Could not read the published radio edition.")
			return
		}
		for field, file := range map[string]string{"dock": "pod-dock.json", "lastRadio": "pod-dock-ready.json"} {
			var d dockReport
			if err := readPodFile(dir, file, &d); err == nil && ((field == "dock" && d.Account == "" && d.Library == "" && (d.Phase == "waiting" || d.Phase == "checking" || d.Phase == "no-edition" || d.Phase == "failed")) || (d.Account == owner && d.Library == library)) {
				out[field] = d
			}
		}
	}
	dataOut(w, 200, out)
}
