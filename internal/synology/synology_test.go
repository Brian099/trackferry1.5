package synology

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoginSongsAndPlaylists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/query.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"SYNO.API.Auth":{"maxVersion":7,"minVersion":1,"path":"entry.cgi"},"SYNO.AudioStation.Song":{"maxVersion":3,"minVersion":1,"path":"AudioStation/song.cgi"},"SYNO.AudioStation.Stream":{"maxVersion":2,"minVersion":1,"path":"AudioStation/stream.cgi"},"SYNO.AudioStation.Cover":{"maxVersion":3,"minVersion":1,"path":"AudioStation/cover.cgi"},"SYNO.AudioStation.Playlist":{"maxVersion":3,"minVersion":1,"path":"AudioStation/playlist.cgi"}},"success":true}`))
	})
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"sid":"sid-1"},"success":true}`))
	})
	mux.HandleFunc("/webapi/AudioStation/song.cgi", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "0" {
			w.Write([]byte(`{"data":{"offset":0,"songs":[{"id":"music_1","path":"/music/音乐/a.mp3","title":"歌1","additional":{"song_audio":{"codec":"mp3","container":"mp3","duration":260,"filesize":100,"frequency":44100},"song_tag":{"album":"专辑","album_artist":"甲","artist":"甲"}}},{"id":"music_2","path":"/music/音乐/b.dsf","title":"歌2","additional":{"song_audio":{"codec":"dsf","container":"dsf","duration":300},"song_tag":{"artist":"甲"}}}],"total":2},"success":true}`))
			return
		}
		w.Write([]byte(`{"data":{"offset":10,"songs":[],"total":2},"success":true}`))
	})
	mux.HandleFunc("/webapi/AudioStation/playlist.cgi", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("method") == "list" {
			w.Write([]byte(`{"data":{"playlists":[{"id":"playlist_personal_normal/new","name":"new"}]},"success":true}`))
			return
		}
		w.Write([]byte(`{"data":{"playlists":[{"songs":[{"id":"music_1","path":"/music/音乐/a.mp3","title":"歌1","additional":{"song_audio":{"codec":"mp3","duration":260},"song_tag":{"artist":"甲"}}}]}]},"success":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := &Manager{path: filepath.Join(t.TempDir(), "private", "ds.json")}
	m.http = srv.Client()
	m.audio = srv.Client()
	if err := m.Login(context.Background(), srv.URL, "u", "p", ""); err != nil {
		t.Fatal(err)
	}
	songs, total, err := m.Songs(context.Background(), 1, 200)
	if err != nil || total != 2 || len(songs) != 2 {
		t.Fatalf("songs: %v %d %v", err, total, songs)
	}
	if songs[0].ID != "music_1" || songs[0].Format != "mp3" || songs[0].Root != "音乐" || songs[0].DurationMS != 260000 {
		t.Errorf("song: %+v", songs[0])
	}
	if songs[1].Format != "" {
		t.Errorf("dsf should stay unplayable: %+v", songs[1])
	}
	ps, err := m.Playlists(context.Background())
	if err != nil || len(ps) != 1 || ps[0].Name != "new" {
		t.Fatalf("playlists: %v %+v", err, ps)
	}
	pl, err := m.PlaylistSongs(context.Background(), "playlist_personal_normal/new")
	if err != nil || len(pl) != 1 || pl[0].ID != "music_1" {
		t.Fatalf("playlist songs: %v %+v", err, pl)
	}
}

func TestAutoRenew(t *testing.T) {
	var logins, songsCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/query.cgi", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"SYNO.API.Auth":{"maxVersion":7,"path":"entry.cgi"},"SYNO.AudioStation.Song":{"maxVersion":3,"path":"AudioStation/song.cgi"}},"success":true}`))
	})
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, _ *http.Request) {
		logins++
		w.Write([]byte(`{"data":{"sid":"sid-` + strconv.Itoa(logins) + `"},"success":true}`))
	})
	mux.HandleFunc("/webapi/AudioStation/song.cgi", func(w http.ResponseWriter, _ *http.Request) {
		songsCalls++
		if songsCalls == 2 { // the first Songs() call after login: the session expired
			w.Write([]byte(`{"error":{"code":119},"success":false}`))
			return
		}
		w.Write([]byte(`{"data":{"songs":[{"id":"music_1","path":"/music/音乐/a.mp3","title":"歌1","additional":{"song_audio":{"codec":"mp3","duration":260},"song_tag":{"artist":"甲"}}}],"total":1},"success":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := &Manager{path: filepath.Join(t.TempDir(), "private", "ds.json")}
	m.http = srv.Client()
	m.audio = srv.Client()
	if err := m.Login(context.Background(), srv.URL, "u", "p", ""); err != nil {
		t.Fatal(err)
	}
	// The session is refused on the first list; Songs re-links and retries on its own.
	songs, total, err := m.Songs(context.Background(), 1, 200)
	if err != nil || total != 1 || len(songs) != 1 || songs[0].ID != "music_1" {
		t.Fatalf("songs after renew: %v %d %+v", err, total, songs)
	}
	if logins != 2 {
		t.Fatalf("logins: %d", logins)
	}
}
