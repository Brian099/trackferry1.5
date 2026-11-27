package navidrome

import (
	"context"
	"errors"
	"strings"

	"trackferry/internal/library"
)

// Identity of Navidrome among the library's sources.
const (
	SourceID     = "navidrome"
	SourcePrefix = "nd:"
	coverPrefix  = "nd-"
)

func (*Manager) ID() string          { return SourceID }
func (*Manager) Prefix() string      { return SourcePrefix }
func (*Manager) CoverPrefix() string { return coverPrefix }
func (*Manager) Label() string       { return "Navidrome" }

// Songs pages the library through Navidrome's native API, which lists every song directly.
func (m *Manager) Songs(ctx context.Context, page, size int) ([]library.Song, int, error) {
	s, err := m.session()
	if err != nil {
		return nil, 0, err
	}
	start := (page - 1) * size
	songs, total, err := m.nativeSongs(ctx, s, start, size)
	if errors.Is(err, ErrAuth) {
		if token, e := m.nativeLogin(ctx, s); e == nil {
			s.Token = token
			m.keepToken(token)
			songs, total, err = m.nativeSongs(ctx, s, start, size)
		}
	}
	if err != nil {
		return nil, 0, err
	}
	out := make([]library.Song, 0, len(songs))
	for _, x := range songs {
		out = append(out, nativeToLibrary(x))
	}
	return out, total, nil
}

// nativeToLibrary converts one native song; Format stays empty for what speakers cannot
// play, and songs whose file is missing are skipped.
func nativeToLibrary(s nativeSong) library.Song {
	if s.Missing {
		return library.Song{}
	}
	format := library.FormatOf(s.Suffix)
	artist := strings.TrimSpace(s.Artist)
	if artist == "" {
		artist = strings.TrimSpace(s.AlbumArtist)
	}
	return library.Song{
		ID:         s.ID,
		Title:      s.Title,
		Artist:     artist,
		Album:      s.Album,
		AlbumID:    s.AlbumID,
		Cover:      s.ID, // getCoverArt accepts the song id
		DurationMS: int64(s.Duration * 1000),
		Format:     format,
		Size:       s.Size,
		SampleRate: s.SampleRate,
		Bits:       s.BitDepth,
		Root:       "Navidrome",
	}
}
