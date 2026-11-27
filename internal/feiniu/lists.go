package feiniu

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Playlist is one of the account's playlists.
type Playlist struct {
	GUID string `json:"guid"`
	Name string `json:"name"`
}

const (
	listPage = 300
	listMax  = 6000 // songs read of one list
)

// Playlists lists the account's playlists.
func (m *Manager) Playlists(ctx context.Context) ([]Playlist, error) {
	c, err := m.client()
	if err != nil {
		return nil, err
	}
	var out struct {
		List []Playlist `json:"list"`
	}
	err = c.call(ctx, http.MethodGet, "/playlist/list", nil, nil, &out)
	m.note(c, err)
	return out.List, err
}

// PlaylistSongs reads a playlist.
func (m *Manager) PlaylistSongs(ctx context.Context, guid string) ([]Song, error) {
	if !idRe.MatchString(guid) {
		return nil, errors.New("bad playlist id")
	}
	return m.all(ctx, "/track/playlist-detail/list", url.Values{"playlistGUID": {guid}})
}

// Favorites reads the account's favourites.
func (m *Manager) Favorites(ctx context.Context) ([]Song, error) {
	return m.all(ctx, "/favorite-track/list", url.Values{"sort": {"favoriteAt,desc"}})
}

func (m *Manager) all(ctx context.Context, path string, q url.Values) ([]Song, error) {
	c, err := m.client()
	if err != nil {
		return nil, err
	}
	var out []Song
	q.Set("size", strconv.Itoa(listPage))
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		var p songPage
		err := c.call(ctx, http.MethodGet, path, q, nil, &p)
		var refused *APIError
		if !errors.As(err, &refused) {
			m.note(c, err)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p.List...)
		if len(p.List) == 0 || len(out) >= p.Total || len(out) >= listMax {
			return out, nil
		}
	}
}
