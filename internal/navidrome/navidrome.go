// Package navidrome talks to a Navidrome server. The song index comes from Navidrome's own
// native API (one page lists every song), while streaming, covers and playlists use the
// Subsonic API. Both sign in with the account's password, which lives in the private file.
package navidrome

import (
	"bytes"
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

// Session is what a login leaves behind. The password signs Subsonic requests; the token
// signs native requests.
type Session struct {
	Base     string    `json:"base"`
	User     string    `json:"user"`
	Password string    `json:"password"`
	Token    string    `json:"token,omitempty"`
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

// Login links an account, verifies it with one Subsonic ping and takes the native token.
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
	token, err := m.nativeLogin(ctx, s)
	if err != nil {
		return err
	}
	s.Token = token
	if err := m.saveSession(s); err != nil {
		return err
	}
	m.mu.Lock()
	m.ses, m.lastErr = s, ""
	m.mu.Unlock()
	return nil
}

// Logout forgets the session.
func (m *Manager) Logout() error {
	m.mu.Lock()
	m.ses, m.lastErr = nil, ""
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

func (m *Manager) session() (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ses == nil {
		return nil, ErrNotLinked
	}
	return m.ses, nil
}

func (m *Manager) saveSession(s *Session) error {
	raw, _ := json.Marshal(s)
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// keepToken persists a renewed native token.
func (m *Manager) keepToken(token string) {
	m.mu.Lock()
	if m.ses != nil {
		m.ses.Token = token
	}
	s := m.ses
	m.mu.Unlock()
	if s != nil {
		m.saveSession(s)
	}
}

// ping asks the Subsonic API whether the account is accepted.
func (m *Manager) ping(ctx context.Context, s *Session) error {
	var inner struct {
		Status string  `json:"status"`
		Error  *subErr `json:"error"`
	}
	if err := get(ctx, m.http, s, "ping", nil, &inner); err != nil {
		return err
	}
	return check(inner.Status, inner.Error)
}

// nativeLogin signs in to Navidrome's own API and returns the bearer token.
func (m *Manager) nativeLogin(ctx context.Context, s *Session) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": s.User, "password": s.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.Base, "/")+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("连不上 Navidrome：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Navidrome 返回 HTTP %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return "", errors.New("Navidrome 没有返回登录凭证")
	}
	return out.Token, nil
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

// media opens a binary Subsonic endpoint (stream, cover).
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

// nativeSong is one track of the native /api/song list.
type nativeSong struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Album       string  `json:"album"`
	AlbumID     string  `json:"albumId"`
	Artist      string  `json:"artist"`
	AlbumArtist string  `json:"albumArtist"`
	Suffix      string  `json:"suffix"`
	Duration    float64 `json:"duration"` // seconds
	Size        int64   `json:"size"`
	SampleRate  int     `json:"sampleRate"`
	BitDepth    int     `json:"bitDepth"`
	Missing     bool    `json:"missing"`
}

// nativeSongs reads one page of the song list and its total.
func (m *Manager) nativeSongs(ctx context.Context, s *Session, start, size int) ([]nativeSong, int, error) {
	u := fmt.Sprintf("%s/api/song?_start=%d&_end=%d&_sort=id", strings.TrimRight(s.Base, "/"), start, start+size)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-ND-Authorization", "Bearer "+s.Token)
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("连不上 Navidrome：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, 0, ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("Navidrome 返回 HTTP %d", resp.StatusCode)
	}
	total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, 0, err
	}
	var songs []nativeSong
	if err := json.Unmarshal(raw, &songs); err != nil {
		return nil, 0, errors.New("Navidrome 返回了看不懂的内容")
	}
	return songs, total, nil
}

// song is one track of the Subsonic API (used for playlists).
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
		if t, ok := subsonicToLibrary(e.Song); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// subsonicToLibrary converts one Subsonic playlist song.
func subsonicToLibrary(s song) (library.Song, bool) {
	if s.IsDir || s.ID == "" || strings.TrimSpace(s.Title) == "" {
		return library.Song{}, false
	}
	format := library.FormatOf(s.Suffix)
	if format == "" {
		return library.Song{}, false
	}
	return library.Song{
		ID:         s.ID,
		Title:      s.Title,
		Artist:     s.Artist,
		Album:      s.Album,
		AlbumID:    s.AlbumID,
		Cover:      s.CoverArt,
		DurationMS: int64(s.Duration) * 1000,
		Format:     format,
		Size:       s.Size,
		SampleRate: s.SamplingRate,
		Bits:       s.BitDepth,
		Root:       "Navidrome",
	}, true
}
