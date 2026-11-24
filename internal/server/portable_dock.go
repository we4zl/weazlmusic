package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The dock uses the workstation's existing SSH transport. This listener only
// exists inside the service container and serves the configured pod library.
func (a *apiV1) startDock() error {
	path := filepath.Join(a.s.cfg.DataDir, "recordings", "dock.sock")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err = os.Chmod(path, 0600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: http.HandlerFunc(a.dockRequest), ReadHeaderTimeout: 5 * time.Second}
	a.wg.Add(2)
	go func() { defer a.wg.Done(); _ = srv.Serve(ln) }()
	go func() { defer a.wg.Done(); <-a.ctx.Done(); _ = srv.Close() }()
	return nil
}

func (a *apiV1) dockRequest(w http.ResponseWriter, r *http.Request) {
	user, lib := a.nightly.Username, a.nightly.Library
	var pod namedPod
	if token := r.Header.Get("X-Weazl-Pod-Key"); token != "" {
		if len(token) != 64 || a.get("named-pod", digest(token), "*", &pod) != nil {
			verror(w, 403, "unknown_player", "Pair this player first.")
			return
		}
		user, lib = pod.Username, pod.Library
	} else if user == "" {
		verror(w, 403, "unpaired", "Pair this player first.")
		return
	}
	se, err := a.deviceIdentity(deviceSession{Username: user, AccessExpires: a.now().Add(3 * time.Minute)})
	if err != nil || se.ServerURL == "" || a.libraryID(se) != lib {
		verror(w, 403, "unavailable", "Dock library unavailable.")
		return
	}
	se.PodID = pod.ID
	se.ctx = r.Context()
	p := r.URL.Path
	id := r.URL.Query().Get("id")
	switch {
	case r.Method == "GET" && p == "/identity" && pod.ID != "":
		dataOut(w, 200, pod)
	case r.Method == "PUT" && p == "/radio-status" && pod.ID != "":
		var report dockReport
		if decode(r, &report) != nil || report.Total < 0 || report.Done < 0 || report.Done > report.Total || len(report.Phase) > 32 || len(report.Name) > 256 {
			verror(w, 422, "invalid", "Invalid recorder receipt.")
			return
		}
		report.Account = accountID(se.User)
		report.Library = lib
		report.Seen = a.now().UTC()
		if err := a.put("pod-radio", pod.ID, report.Account, report); err != nil {
			verror(w, 503, "storage", "Cannot save recorder receipt.")
			return
		}
		if report.Phase == "ready" {
			if err := a.put("pod-radio-ready", pod.ID, report.Account, report); err != nil {
				verror(w, 503, "storage", "Cannot save recorder receipt.")
				return
			}
		}
		w.WriteHeader(204)
	case r.Method == "PUT" && p == "/verify-result" && se.PodID != "":
		a.verifyRequest(w, r, se)
	case r.Method == "GET" && p == "/radio-edition" && se.PodID != "":
		a.receiptRadio(w, r, se)
	case r.Method == "GET" && p == "/sources":
		// List only browser queues from this account and this library.
		prefix := se.stateKey() + ":browser:"
		rows, e := a.db.Query("SELECT id,body FROM records WHERE owner=? AND kind='queue' AND substr(id,1,?)=?", accountID(se.User), len([]rune(prefix)), prefix)
		if e != nil {
			verror(w, 503, "storage_unavailable", "Could not read queues.")
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var key string
			var raw []byte
			var q userState
			if rows.Scan(&key, &raw) != nil || a.open(raw, &q) != nil {
				verror(w, 503, "storage_unavailable", "Could not read queue.")
				return
			}
			var track struct{ Title, Artist string }
			if json.Unmarshal(q.Current, &track) == nil && track.Title != "" {
				out = append(out, map[string]any{"device": strings.TrimPrefix(key, prefix), "title": track.Title, "artist": track.Artist, "count": 1 + len(q.Queue)})
			}
		}
		if rows.Err() != nil {
			verror(w, 503, "storage_unavailable", "Could not list queues.")
			return
		}
		dataOut(w, 200, out)
	case r.Method == "PUT" && p == "/source":
		a.followDockBrowser(w, se, id)
	case r.Method == "GET" && p == "/queue":
		source, q, e := a.dockBookmark(se)
		if e != nil {
			verror(w, 503, "storage_unavailable", "Could not read source.")
			return
		}
		sends, err := a.pendingPodSends(se)
		if err != nil {
			verror(w, 503, "storage_unavailable", "Could not read the pod sync queue.")
			return
		}
		if se.PodID != "" && r.URL.Query().Get("receipts") == "1" {
			a.receiptQueue(w, r, se, source, q, sends)
			return
		}
		dataOut(w, 200, map[string]any{"source": source, "current": q.Current, "queue": q.Queue, "sends": sends})
	case r.Method == "POST" && p == "/delivered":
		a.mu.Lock()
		defer a.mu.Unlock()
		a.confirmPodSends(w, r, se)
	case r.Method == "GET" && p == "/recent":
		result, e := a.s.call(r.Context(), se, "getAlbumList2", url.Values{"type": {"newest"}, "size": {"12"}})
		if e != nil {
			verror(w, 502, "upstream_unavailable", "Recent albums unavailable.")
			return
		}
		if se.PodID != "" && r.URL.Query().Get("receipts") == "1" {
			var albums struct {
				Album []struct {
					ID, CoverArt, Changed string
					SongCount             int
				}
			}
			if err := json.Unmarshal(result["albumList2"], &albums); err != nil {
				verror(w, 502, "upstream", "Invalid albums.")
				return
			}
			raw, _ := json.Marshal(albums)
			rev := digest(string(raw))
			a.podArt.Store(se.PodID, artRevision{rev, a.now()})
			dataOut(w, 200, map[string]any{"albums": result["albumList2"], "revision": rev})
			return
		}
		dataOut(w, 200, result["albumList2"])
	case r.Method == "GET" && p == "/audio" && id != "":
		a.s.downloadTrack(w, r, se)
	case r.Method == "GET" && p == "/cover" && id != "":
		a.podCover(w, r, se, mediaLease{Resource: id})
	case r.Method == "GET" && (p == "/api/v1/me" || p == "/api/v1/library/tracks" || p == "/api/v1/library/albums" || strings.HasPrefix(p, "/api/v1/library/playlists/")):
		a.dispatch(w, r, se)
	case r.Method == "PUT" && p == "/api/v1/portable/status":
		a.mu.Lock()
		defer a.mu.Unlock()
		a.portableStatus(w, r, se)
	case (r.Method == "POST" && p == "/api/v1/library/scrobbles") || (r.Method == "PUT" && strings.HasPrefix(p, "/api/v1/library/favorites/track/")):
		a.mutation(w, r, se, a.dispatch)
	default:
		http.NotFound(w, r)
	}
}
