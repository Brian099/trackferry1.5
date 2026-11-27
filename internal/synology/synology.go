// Package synology talks to a Synology Audio Station through the DSM WebAPI: the track
// index, covers, playlists and the audio itself. API paths and versions are discovered with
// SYNO.API.Info, never hard-coded, because they vary between DSM releases.
package synology

import (
	"context"
	"crypto/tls"
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

// Errors callers tell apart.
var (
	ErrNotLinked = errors.New("还没有连接群晖音乐")
	ErrAuth      = errors.New("群晖音乐登录已失效")
)

// Status is what the management page shows. It never contains the password.
type Status struct {
	Linked   bool      `json:"linked"`
	Base     string    `json:"base,omitempty"`
	User     string    `json:"user,omitempty"`
	LinkedAt time.Time `json:"linked_at,omitzero"`
	Expired  bool      `json:"expired"`
	Error    string    `json:"error,omitempty"`
}

// Session is what a login leaves behind. The password is kept to re-login when the sid
// expires, and for two-step verification the owner has to sign in again by hand.
type Session struct {
	Base     string    `json:"base"`
	User     string    `json:"user"`
	Password string    `json:"password"`
	SID      string    `json:"sid"`
	LinkedAt time.Time `json:"linked_at"`
}

// apiSpec is a discovered API path and its newest supported version.
type apiSpec struct {
	path       string
	maxVersion int
}

// Manager holds the linked account. It is safe for concurrent use.
type Manager struct {
	path string

	mu      sync.RWMutex
	ses     *Session
	http    *http.Client
	audio   *http.Client
	apis    map[string]apiSpec
	expired bool
	lastErr string
}

// NewManager loads a previous session from path.
func NewManager(path string) (*Manager, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // LAN self-signed certs
	m := &Manager{path: path, apis: map[string]apiSpec{},
		http:  &http.Client{Transport: tr, Timeout: 30 * time.Second},
		audio: &http.Client{Transport: tr}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil || s.Base == "" || s.SID == "" {
		return m, nil
	}
	m.ses = &s
	return m, nil
}

// Base normalises what the user typed into a DSM address.
func Base(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("请填群晖地址")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", errors.New("群晖地址不对，例如 http://192.168.1.10:5000")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("群晖地址只支持 http 或 https")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
}

// Login discovers the APIs, signs in and verifies the library opens.
func (m *Manager) Login(ctx context.Context, base, user, password, otp string) error {
	base, err := Base(base)
	if err != nil {
		return err
	}
	user = strings.TrimSpace(user)
	if user == "" || password == "" {
		return errors.New("请填群晖音乐的用户名和密码")
	}
	apis, err := m.discover(ctx, base)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.apis = apis
	m.mu.Unlock()
	auth, ok := apis["SYNO.API.Auth"]
	if !ok {
		return errors.New("这台群晖没有登录接口")
	}
	q := url.Values{"api": {"SYNO.API.Auth"}, "version": {strconv.Itoa(auth.maxVersion)}, "method": {"login"},
		"account": {user}, "passwd": {password}, "session": {"AudioStation"}, "format": {"sid"}}
	if strings.TrimSpace(otp) != "" {
		q.Set("otp_code", strings.TrimSpace(otp))
	}
	var out struct {
		Success bool `json:"success"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
		Data struct {
			SID string `json:"sid"`
		} `json:"data"`
	}
	if err := m.call(ctx, base, auth.path, q, &out); err != nil {
		return err
	}
	if !out.Success {
		return synoError(out.Error.Code)
	}
	if out.Data.SID == "" {
		return errors.New("群晖没有返回会话")
	}
	s := &Session{Base: base, User: user, Password: password, SID: out.Data.SID, LinkedAt: time.Now().UTC()}
	// The session must already open the library.
	if _, _, err := m.songs(ctx, s, 0, 1); err != nil {
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
	m.ses, m.expired, m.lastErr = s, false, ""
	m.mu.Unlock()
	return nil
}

// Logout forgets the session.
func (m *Manager) Logout() error {
	m.mu.Lock()
	m.ses, m.expired, m.lastErr = nil, false, ""
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
	return Status{Linked: true, Base: m.ses.Base, User: m.ses.User, LinkedAt: m.ses.LinkedAt, Expired: m.expired, Error: m.lastErr}
}

// Linked reports whether an account is linked.
func (m *Manager) Linked() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ses != nil
}

// Ready reports whether audio can be expected to open.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ses != nil && !m.expired
}

func (m *Manager) session() (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ses == nil {
		return nil, ErrNotLinked
	}
	return m.ses, nil
}

// note marks the link expired when a session was refused.
func (m *Manager) note(err error) {
	if errors.Is(err, ErrAuth) {
		m.mu.Lock()
		m.expired = true
		m.mu.Unlock()
	}
}

// renew signs in again with the stored password. A two-step-verified account cannot renew
// on its own and needs the owner to sign in with the code again.
func (m *Manager) renew(ctx context.Context) error {
	m.mu.RLock()
	s := m.ses
	m.mu.RUnlock()
	if s == nil || s.Password == "" {
		return ErrNotLinked
	}
	return m.Login(ctx, s.Base, s.User, s.Password, "")
}

// Run re-links a session that has expired, so a NAS that drops sessions comes back on its own.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if st := m.Status(); st.Linked && st.Expired {
			c, cancel := context.WithTimeout(ctx, 15*time.Second)
			m.renew(c)
			cancel()
		}
	}
}

// discover asks SYNO.API.Info for the paths and versions we need.
func (m *Manager) discover(ctx context.Context, base string) (map[string]apiSpec, error) {
	names := "SYNO.API.Auth,SYNO.AudioStation.Song,SYNO.AudioStation.Stream,SYNO.AudioStation.Cover,SYNO.AudioStation.Playlist,SYNO.AudioStation.Lyrics"
	var out struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Path       string `json:"path"`
			MaxVersion int    `json:"maxVersion"`
		} `json:"data"`
	}
	q := url.Values{"api": {"SYNO.API.Info"}, "version": {"1"}, "method": {"query"}, "query": {names}}
	if err := m.call(ctx, base, "query.cgi", q, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, errors.New("群晖 API 信息获取失败")
	}
	apis := map[string]apiSpec{}
	for name, d := range out.Data {
		if d.Path != "" {
			apis[name] = apiSpec{path: d.Path, maxVersion: d.MaxVersion}
		}
	}
	return apis, nil
}

// call performs a JSON WebAPI request.
func (m *Manager) call(ctx context.Context, base, path string, q url.Values, out any) error {
	u := base + "/webapi/" + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("连不上群晖：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("群晖返回 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("群晖返回了看不懂的内容")
	}
	return nil
}

// synoError turns a DSM error code into a message.
func synoError(code int) error {
	switch code {
	case 400:
		return errors.New("群晖拒绝了请求：账号或参数不对")
	case 401:
		return errors.New("群晖拒绝了请求：账号被停用")
	case 402:
		return errors.New("群晖拒绝了请求：权限不足")
	case 403:
		return errors.New("群晖需要两步验证码")
	case 404:
		return errors.New("群晖没有接受这个两步验证码")
	case 105, 119:
		return ErrAuth
	default:
		return fmt.Errorf("群晖返回错误码 %d", code)
	}
}

// spec returns a discovered API or an error.
func (m *Manager) spec(name string) (apiSpec, error) {
	m.mu.RLock()
	a, ok := m.apis[name]
	m.mu.RUnlock()
	if !ok {
		return apiSpec{}, fmt.Errorf("群晖没有 %s 接口", name)
	}
	return a, nil
}

// song is one track of SYNO.AudioStation.Song list.
type song struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	Additional struct {
		SongAudio struct {
			Bitrate   int    `json:"bitrate"`
			Codec     string `json:"codec"`
			Container string `json:"container"`
			Duration  int    `json:"duration"` // seconds
			FileSize  int64  `json:"filesize"`
			Frequency int    `json:"frequency"`
		} `json:"song_audio"`
		SongTag struct {
			Album       string `json:"album"`
			AlbumArtist string `json:"album_artist"`
			Artist      string `json:"artist"`
			Genre       string `json:"genre"`
			Year        int    `json:"year"`
		} `json:"song_tag"`
	} `json:"additional"`
}

// Playlist is one playlist of SYNO.AudioStation.Playlist.
type Playlist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// songs reads one page of the library.
func (m *Manager) songs(ctx context.Context, s *Session, offset, limit int) ([]song, int, error) {
	a, err := m.spec("SYNO.AudioStation.Song")
	if err != nil {
		return nil, 0, err
	}
	q := url.Values{"api": {"SYNO.AudioStation.Song"}, "version": {strconv.Itoa(a.maxVersion)}, "method": {"list"},
		"library": {"all"}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)},
		"additional": {"song_tag,song_audio"}, "_sid": {s.SID}}
	var out struct {
		Success bool `json:"success"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
		Data struct {
			Songs []song `json:"songs"`
			Total int    `json:"total"`
		} `json:"data"`
	}
	if err := m.call(ctx, s.Base, a.path, q, &out); err != nil {
		return nil, 0, err
	}
	if !out.Success {
		err := synoError(out.Error.Code)
		m.note(err)
		return nil, 0, err
	}
	return out.Data.Songs, out.Data.Total, nil
}

// Open opens a song's audio.
func (m *Manager) Open(ctx context.Context, id, rng string) (*http.Response, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	a, err := m.spec("SYNO.AudioStation.Stream")
	if err != nil {
		return nil, err
	}
	q := url.Values{"api": {"SYNO.AudioStation.Stream"}, "version": {strconv.Itoa(a.maxVersion)}, "method": {"stream"},
		"id": {id}, "_sid": {s.SID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Base+"/webapi/"+a.path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := m.audio.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上群晖：%w", err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
		return resp, nil
	}
	resp.Body.Close()
	return nil, fmt.Errorf("群晖返回 HTTP %d", resp.StatusCode)
}

// Cover opens a song's cover image.
func (m *Manager) Cover(ctx context.Context, id string) (*http.Response, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	a, err := m.spec("SYNO.AudioStation.Cover")
	if err != nil {
		return nil, err
	}
	q := url.Values{"api": {"SYNO.AudioStation.Cover"}, "version": {strconv.Itoa(a.maxVersion)}, "method": {"getsongcover"},
		"id": {id}, "_sid": {s.SID}}
	resp, err := m.http.Get(s.Base + "/webapi/" + a.path + "?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("连不上群晖：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("群晖返回 HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// Playlists lists the account's playlists.
func (m *Manager) Playlists(ctx context.Context) ([]Playlist, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	a, err := m.spec("SYNO.AudioStation.Playlist")
	if err != nil {
		return nil, err
	}
	q := url.Values{"api": {"SYNO.AudioStation.Playlist"}, "version": {strconv.Itoa(a.maxVersion)}, "method": {"list"}, "_sid": {s.SID}}
	var out struct {
		Success bool `json:"success"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
		Data struct {
			Playlists []Playlist `json:"playlists"`
		} `json:"data"`
	}
	if err := m.call(ctx, s.Base, a.path, q, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, synoError(out.Error.Code)
	}
	return out.Data.Playlists, nil
}

// PlaylistSongs reads a playlist as source-neutral songs.
func (m *Manager) PlaylistSongs(ctx context.Context, id string) ([]library.Song, error) {
	s, err := m.session()
	if err != nil {
		return nil, err
	}
	a, err := m.spec("SYNO.AudioStation.Playlist")
	if err != nil {
		return nil, err
	}
	q := url.Values{"api": {"SYNO.AudioStation.Playlist"}, "version": {strconv.Itoa(a.maxVersion)}, "method": {"getinfo"},
		"id": {id}, "additional": {"song_tag,song_audio"}, "_sid": {s.SID}}
	var out struct {
		Success bool `json:"success"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
		Data struct {
			Playlists []struct {
				Songs []song `json:"songs"`
			} `json:"playlists"`
		} `json:"data"`
	}
	if err := m.call(ctx, s.Base, a.path, q, &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, synoError(out.Error.Code)
	}
	res := []library.Song{}
	for _, p := range out.Data.Playlists {
		for _, x := range p.Songs {
			if t, ok := toLibrarySong(x); ok {
				res = append(res, t)
			}
		}
	}
	return res, nil
}
