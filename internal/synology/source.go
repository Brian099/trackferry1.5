package synology

import (
	"context"
	"errors"
	"strings"

	"trackferry/internal/library"
)

// Identity of 群晖 Audio Station among the library's sources.
const (
	SourceID     = "synology"
	SourcePrefix = "ds:"
	coverPrefix  = "ds-"
)

func (*Manager) ID() string          { return SourceID }
func (*Manager) Prefix() string      { return SourcePrefix }
func (*Manager) CoverPrefix() string { return coverPrefix }
func (*Manager) Label() string       { return "群晖音乐" }

// Songs pages the library, renewing the session once when it has expired.
func (m *Manager) Songs(ctx context.Context, page, size int) ([]library.Song, int, error) {
	s, err := m.session()
	if err != nil {
		return nil, 0, err
	}
	songs, total, err := m.songs(ctx, s, (page-1)*size, size)
	if errors.Is(err, ErrAuth) {
		if rerr := m.renew(ctx); rerr == nil {
			if s, err = m.session(); err == nil {
				songs, total, err = m.songs(ctx, s, (page-1)*size, size)
			}
		}
	}
	if err != nil {
		return nil, 0, err
	}
	out := make([]library.Song, 0, len(songs))
	for _, x := range songs {
		out = append(out, toLibrarySong(x))
	}
	return out, total, nil
}

// toLibrarySong converts one 群晖 song; Format stays empty for what speakers cannot play.
func toLibrarySong(s song) library.Song {
	format := library.FormatOf(s.Additional.SongAudio.Codec)
	if format == "" {
		format = library.FormatOf(s.Additional.SongAudio.Container)
	}
	artist := s.Additional.SongTag.Artist
	if artist == "" {
		artist = s.Additional.SongTag.AlbumArtist
	}
	return library.Song{
		ID:         s.ID,
		Title:      s.Title,
		Artist:     artist,
		Album:      s.Additional.SongTag.Album,
		AlbumID:    s.Additional.SongTag.Album,
		Cover:      s.ID, // getsongcover takes the song id
		DurationMS: int64(s.Additional.SongAudio.Duration) * 1000,
		Format:     format,
		Size:       s.Additional.SongAudio.FileSize,
		SampleRate: s.Additional.SongAudio.Frequency,
		Root:       rootOf(s.Path),
	}
}

// rootOf names the shared folder a song lies in: "/music/音乐/…" is 音乐.
func rootOf(p string) string {
	f := strings.Split(strings.Trim(p, "/"), "/")
	if len(f) >= 3 {
		return f[1]
	}
	return "群晖音乐"
}
