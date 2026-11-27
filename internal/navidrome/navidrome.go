// Package navidrome talks to a Navidrome (Subsonic API) server: the track index, covers,
// playlists and the audio itself. Subsonic is stateless, so the account's password is kept
// to sign each request; it lives in the private file, never in logs.
package navidrome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"trackferry/internal/library"
)

const (
	apiVersion = "1.16.1"
	clientName = "trackferry"
	pageMax    = 500 // albums per getAlbumList2 page
)

// Errors callers tell apart.
var (
	ErrNotLinked = errors.New("还没有连接 Navidrome")
	ErrAuth      = errors.New("Navidrome 登录已失效")
)

// Status is what the management page shows. It never contains the password.
type Status struct {
	Linked   bool      `json:"linked"`
	Base     string    `json:"base,omitempty"`
	User     string    `json:"user,omitempty"`
	LinkedAt time.Time `json:"linked_at,omitzero"`
	Error    string    `json:"error,omitempty"`
}

// Session is what a login leaves behind.
type Session struct {
	Base     string    `json:"base"`
	User     string    `json:"user"`
	Password string    `json:"password"` // kept to sign stateless Subsonic requests
	LinkedAt time.Time `json:"linked_at"`
}

// Manager holds the linked account. It is safe for concurrent use.
type Manager struct {
	path string

	mu      sync.RWMutex
	ses     *Session
	http    *http.Client // JSON and images, with a timeout
	audio   *http.Client // streamed audio: no overall timeout
	lastErr string
	cache   []library.Song // the last enumeration, paged out by Songs
}

// NewManager loads a previous session from path.
func NewManager(path string) (*Manager, error) {
	m := &Manager{path: path, http: &http.Client{Timeout: 2 * time.Minute}, audio: &http.Client{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil || s.Base == "" || s.User == "" {
		return m, nil
	}
	m.ses = &s
	return m, nil
}

// Base normalises what the user typed into a Navidrome address.
func Base(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("请填 Navidrome 地址")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", errors.New("Navidrome 地址不对，例如 http://192.168.1.10:4533")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Navidrome 地址只支持 http 或 https")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
}

// Login links an account and verifies it with one ping.
func (m *Manager) Login(ctx context.Context, base, user, password string) error {
	base, err := Base(base)
	if err != nil {
		return err
	}
	user = strings.TrimSpace(user)
	if user == "" || password == "" {
		return errors.New("请填 Navidrome 的用户名和密码")
	}
	s := &Session{Base: base, User: user, Password: password, LinkedAt: time.Now().UTC()}
	if err := m.ping(ctx, s); err != nil {
		return err
	}
	raw, _ := json.Marshal(s)
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return err
	}
	m.mu.Lock()
	m.ses, m.lastErr, m.cache = s, "", nil
	m.mu.Unlock()
	return nil
}

// Logout forgets the session.
func (m *Manager) Logout() error {
	m.mu.Lock()
	m.ses, m.lastErr, m.cache = nil, "", nil
	m.mu.Unlock()
	if err := os.Remove(m.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Status reports the link.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ses == nil {
		return Status{}
	}
	return Status{Linked: true, Base: m.ses.Base, User: m.ses.User, LinkedAt: m.ses.LinkedAt, Error: m.lastErr}
}

// Linked reports whether an account is linked.
func (m *Manager) Linked() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ses != nil
}

// Ready reports whether audio can be expected to open.
func (m *Manager) Ready() bool { return m.Linked() }

// session returns the session, or an error.
func (m *Manager) session() (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ses == nil {
		return nil, ErrNotLinked
	}
	return m.ses, nil
}

// ping asks the server whether the account is accepted.
func (m *Manager) ping(ctx context.Context, s *Session) error {
	var inner struct {
		Status string `json:"status"`
		Error  *subErr `json:"error"`
	}
	if err := get(ctx, m.http, s, "ping", nil, &inner); err != nil {
		return err
	}
	if err := check(inner.Status, inner.Error); err != nil {
		return err
	}
	return nil
}

// subErr is a Subsonic error object.
type subErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func check(status string, e *subErr) error {
	if e != nil && e.Code != 0 {
		if e.Code == 40 {
			return ErrAuth
		}
		return fmt.Errorf("Navidrome 拒绝了请求（%d %s）", e.Code, e.Message)
	}
	if status != "ok" {
		return errors.New("Navidrome 没有接受请求")
	}
	return nil
}

// get calls a JSON Subsonic endpoint and decodes the subsonic-response into inner.
func get(ctx context.Context, hc *http.Client, s *Session, endpoint string, q url.Values, inner any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("u", s.User)
	q.Set("p", s.Password)
	q.Set("v", apiVersion)
	q.Set("c", clientName)
	q.Set("f", "json")
	u := strings.TrimRight(s.Base, "/") + "/rest/" + endpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 Navidrome：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Navidrome 返回 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var outer struct {
		SubsonicResponse json.RawMessage `json:"subsonic-response"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil || len(outer.SubsonicResponse) == 0 {
		return errors.New("Navidrome 返回了看不懂的内容")
	}
	return json.Unmarshal(outer.SubsonicResponse, inner)
}

// media opens a binary endpoint (stream, cover). Refusals arrive as JSON.
func media(ctx context.Context, hc *http.Client, s *Session, endpoint string, q url.Values, rng string) (*http.Response, error) {
	q.Set("u", s.User)
	q.Set("p", s.Password)
	q.Set("v", apiVersion)
	q.Set("c", clientName)
	q.Set("f", "json")
	u := strings.TrimRight(s.Base, "/") + "/rest/" + endpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 Navidrome：%w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrAuth
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("Navidrome 返回 HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// album is one album of getAlbumList2.
type album struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Artist   string `json:"artist"`
	CoverArt string `json:"coverArt"`
}

// song is one track of getAlbum.
type song struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Album        string `json:"album"`
	Artist       string `json:"artist"`
	AlbumID      string `json:"albumId"`
	CoverArt     string `json:"coverArt"`
	Suffix       string `json:"suffix"`
	Duration     int    `json:"duration"` // seconds
	Size         int64  `json:"size"`
	SamplingRate int    `json:"samplingRate"`
	BitDepth     int    `json:"bitDepth"`
	IsDir        bool   `json:"isDir"`
}

// Playlist is one playlist.
type Playlist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// albumList pages the alphabetical album list.
func (m *Manager) albumList(ctx context.Context, s *Session, offset, size int) ([]album, error) {
	var inner struct {
		Status     string  `json:"status"`
		Error      *subErr `json:"error"`
		AlbumList2 struct {
			Album []album `json:"album"`
		} `json:"albumList2"`
	}
	q := url.Values{"type": {"alphabeticalByName"}, "size": {strconv.Itoa(size)}, "offset": {strconv.Itoa(offset)}}
	if err := get(ctx, m.http, s, "getAlbumList2", q, &inner); err != nil {
		return nil, err
	}
	if err := check(inner.Status, inner.Error); err != nil {
		return nil, err
	}
	return inner.AlbumList2.Album, nil
}

// albumSongs reads one album's songs.
func (m *Manager) albumSongs(ctx context.Context, s *Session, id string) ([]song, error) {
	var inner struct {
		Status string  `json:"status"`
		Error  *subErr `json:"error"`
		Album  struct {
			Song []song `json:"song"`
		} `json:"album"`
	}
	if err := get(ctx, m.http, s, "getAlbum", url.Values{"id": {id}}, &inner); err != nil {
		return nil, err
	}
	if err := check(inner.Status, inner.Error); err != nil {
		return nil, err
	}
	return inner.Album.Song, nil
}

// enumerate reads every album and its songs. Subsonic has no "all songs" call, so this is
// one request per album.
func (m *Manager) enumerate(ctx context.Context, s *Session) ([]library.Song, error) {
	var albums []album
	for offset := 0; ; offset += pageMax {
		batch, err := m.albumList(ctx, s, offset, pageMax)
		if err != nil {
			return nil, err
		}
		albums = append(albums, batch...)
		if len(batch) < pageMax {
			break
		}
	}
	out := []library.Song{}
	for _, a := range albums {
		songs, err := m.albumSongs(ctx, s, a.ID)
		if err != nil {
			// One retry: a remote server can hiccup while we walk every album.
			songs, err = m.albumSongs(ctx, s, a.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("读专辑 %q 失败：%w", a.Name, err)
		}
		for _, x := range songs {
			if t, ok := toLibrarySong(x); ok {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// Open opens a song's audio.
func (m *Manager) Open(ctx context.Context, id, rng string) (*http.Response, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	return media(ctx, m.audio, s, "stream", url.Values{"id": {id}}, rng)
}

// Cover opens a cover image.
func (m *Manager) Cover(ctx context.Context, id, size string) (*http.Response, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	q := url.Values{"id": {id}}
	if size != "" {
		q.Set("size", size)
	}
	return media(ctx, m.http, s, "getCoverArt", q, "")
}

// Playlists lists the account's playlists.
func (m *Manager) Playlists(ctx context.Context) ([]Playlist, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	var inner struct {
		Status    string     `json:"status"`
		Error     *subErr    `json:"error"`
		Playlists struct {
			Playlist []Playlist `json:"playlist"`
		} `json:"playlists"`
	}
	if err := get(ctx, m.http, s, "getPlaylists", nil, &inner); err != nil {
		return nil, err
	}
	if err := check(inner.Status, inner.Error); err != nil {
		return nil, err
	}
	return inner.Playlists.Playlist, nil
}

// PlaylistSongs reads a playlist as source-neutral songs.
func (m *Manager) PlaylistSongs(ctx context.Context, id string) ([]library.Song, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	var inner struct {
		Status   string  `json:"status"`
		Error    *subErr `json:"error"`
		Playlist struct {
			Entry []struct {
				Song song `json:"song"`
			} `json:"entry"`
		} `json:"playlist"`
	}
	if err := get(ctx, m.http, s, "getPlaylist", url.Values{"id": {id}}, &inner); err != nil {
		return nil, err
	}
	if err := check(inner.Status, inner.Error); err != nil {
		return nil, err
	}
	out := []library.Song{}
	for _, e := range inner.Playlist.Entry {
		if t, ok := toLibrarySong(e.Song); ok {
			out = append(out, t)
		}
	}
	return out, nil
}
