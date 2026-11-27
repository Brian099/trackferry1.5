package navidrome

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestEnumerateAndPlaylists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	})
	mux.HandleFunc("/rest/getAlbumList2", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "0" {
			w.Write([]byte(`{"subsonic-response":{"status":"ok","albumList2":{"album":[]}}}`))
			return
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok","albumList2":{"album":[{"id":"al1","name":"甲","artist":"甲","coverArt":"al-al1"},{"id":"al2","name":"乙","artist":"乙","coverArt":"al-al2"}]}}}`))
	})
	mux.HandleFunc("/rest/getAlbum", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("id") {
		case "al1":
			w.Write([]byte(`{"subsonic-response":{"status":"ok","album":{"song":[{"id":"s1","title":"歌1","artist":"甲","album":"甲","albumId":"al1","coverArt":"mf-s1","suffix":"mp3","duration":260,"size":100,"samplingRate":44100,"bitDepth":0},{"id":"s2","title":"歌2","artist":"甲","album":"甲","albumId":"al1","coverArt":"mf-s2","suffix":"flac","duration":300,"size":200,"samplingRate":96000,"bitDepth":24}]}}}`))
		case "al2":
			w.Write([]byte(`{"subsonic-response":{"status":"ok","album":{"song":[{"id":"s3","title":"歌3","artist":"乙","album":"乙","albumId":"al2","coverArt":"mf-s3","suffix":"dsf","duration":300,"size":300}]}}}`))
		default:
			w.Write([]byte(`{"subsonic-response":{"status":"ok","album":{"song":[]}}}`))
		}
	})
	mux.HandleFunc("/rest/getPlaylists", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok","playlists":{"playlist":[{"id":"p1","name":"开车"}]}}}`))
	})
	mux.HandleFunc("/rest/getPlaylist", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"subsonic-response":{"status":"ok","playlist":{"entry":[{"song":{"id":"s1","title":"歌1","artist":"甲","album":"甲","albumId":"al1","coverArt":"mf-s1","suffix":"mp3","duration":260}}]}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := &Manager{path: filepath.Join(t.TempDir(), "private", "nd.json"), http: srv.Client()}
	if err := m.Login(context.Background(), srv.URL, "u", "p"); err != nil {
		t.Fatal(err)
	}
	songs, total, err := m.Songs(context.Background(), 1, 200)
	if err != nil || total != 2 || len(songs) != 2 {
		t.Fatalf("songs: %v %d %v", err, total, songs)
	}
	if songs[0].ID != "s1" || songs[0].Format != "mp3" || songs[0].Root != "Navidrome" || songs[0].DurationMS != 260000 {
		t.Errorf("song: %+v", songs[0])
	}
	if songs[1].Format != "flac" || songs[1].SampleRate != 96000 {
		t.Errorf("flac: %+v", songs[1])
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
