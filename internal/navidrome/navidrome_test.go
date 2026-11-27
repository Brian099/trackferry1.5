package navidrome

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestNativeSongsAndPlaylists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	})
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "post", http.StatusMethodNotAllowed)
			return
		}
		w.Write([]byte(`{"token":"tok-1"}`))
	})
	mux.HandleFunc("/api/song", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ND-Authorization") != "Bearer tok-1" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Total-Count", "3")
		if r.URL.Query().Get("_start") != "0" {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`[
			{"id":"s1","title":"歌1","artist":"甲","album":"A","albumId":"al1","suffix":"mp3","duration":260,"size":100,"sampleRate":44100,"bitDepth":0,"missing":false},
			{"id":"s2","title":"歌2","artist":"甲","album":"A","albumId":"al1","suffix":"flac","duration":300,"size":200,"sampleRate":96000,"bitDepth":24,"missing":false},
			{"id":"s3","title":"歌3","artist":"乙","album":"B","albumId":"al2","suffix":"dsf","duration":300,"size":300,"missing":false}
		]`))
	})
	mux.HandleFunc("/rest/getPlaylists", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok","playlists":{"playlist":[{"id":"p1","name":"开车"}]}}}`))
	})
	mux.HandleFunc("/rest/getPlaylist", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"entry":[{"song":{"id":"s1","title":"歌1","artist":"甲","album":"A","albumId":"al1","coverArt":"mf-s1","suffix":"mp3","duration":260}}]}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := &Manager{path: filepath.Join(t.TempDir(), "private", "nd.json"), http: srv.Client(), audio: srv.Client()}
	if err := m.Login(context.Background(), srv.URL, "u", "p"); err != nil {
		t.Fatal(err)
	}
	songs, total, err := m.Songs(context.Background(), 1, 200)
	if err != nil || total != 3 || len(songs) != 3 {
		t.Fatalf("songs: %v %d %v", err, total, songs)
	}
	if songs[0].Format != "mp3" || songs[0].DurationMS != 260000 {
		t.Errorf("mp3: %+v", songs[0])
	}
	if songs[1].Format != "flac" || songs[1].SampleRate != 96000 {
		t.Errorf("flac: %+v", songs[1])
	}
	if songs[2].Format != "" {
		t.Errorf("dsf should stay unplayable: %+v", songs[2])
	}
	if songs2, total2, err := m.Songs(context.Background(), 2, 200); err != nil || total2 != 3 || len(songs2) != 0 {
		t.Fatalf("page2: %v %d %v", err, total2, songs2)
	}
	ps, err := m.Playlists(context.Background())
	if err != nil || len(ps) != 1 || ps[0].Name != "开车" {
		t.Fatalf("playlists: %v %+v", err, ps)
	}
	pl, err := m.PlaylistSongs(context.Background(), "p1")
	if err != nil || len(pl) != 1 || pl[0].ID != "s1" {
		t.Fatalf("playlist songs: %v %+v", err, pl)
	}
}
