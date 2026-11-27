package feiniu

import (
	"context"
	"path"
	"strings"

	"trackferry/internal/library"
)

// Identity of 飞牛音乐 among the library's sources.
const (
	SourceID     = "feiniu"
	SourcePrefix = "fn:"
)

// Source adapts a Manager to library.Source. Optional capabilities (Favorite, Cover,
// Lyrics, Playlists) are promoted from the embedded Manager.
type Source struct{ *Manager }

func (Source) ID() string          { return SourceID }
func (Source) Prefix() string      { return SourcePrefix }
func (Source) CoverPrefix() string { return "" }
func (Source) Label() string       { return "飞牛音乐" }

// Songs pages the library as source-neutral songs. Unplayable songs keep an empty Format;
// the library skips and counts them.
func (s Source) Songs(ctx context.Context, page, size int) ([]library.Song, int, error) {
	c, err := s.client()
	if err != nil {
		return nil, 0, err
	}
	p, err := c.songs(ctx, page, size)
	s.note(c, err)
	if err != nil {
		return nil, 0, err
	}
	out := make([]library.Song, 0, len(p.List))
	for _, song := range p.List {
		out = append(out, toLibrarySong(song))
	}
	return out, p.Total, nil
}

// toLibrarySong converts one 飞牛 song; Format stays empty for what speakers cannot play.
func toLibrarySong(s Song) library.Song {
	format := library.FormatOf(path.Ext(s.Audio.Path))
	if format == "" {
		format = library.FormatOf(s.Audio.Format)
	}
	if format == "" {
		format = library.FormatOf(s.Audio.Container)
	}
	if strings.EqualFold(s.Audio.Codec, "alac") {
		format = "alac"
	}
	if s.Cue || strings.Contains(strings.ToLower(s.Audio.Codec), "dts") {
		format = ""
	}
	var as []string
	for _, a := range s.Artists {
		if a.Name != "" {
			as = append(as, a.Name)
		}
	}
	out := library.Song{
		ID:         s.GUID,
		Title:      s.Title,
		Artist:     strings.Join(as, "/"),
		Cover:      s.CoverID,
		DurationMS: int64(s.Duration),
		Format:     format,
		Size:       s.Audio.Size,
		SampleRate: s.Audio.SampleRate,
		Bits:       s.Audio.Bits,
		Favorite:   s.Favorite,
		AddedMS:    s.Created * 1000,
		Root:       libraryOf(s.Audio.Path),
	}
	if s.Album != nil {
		out.Album, out.AlbumID = s.Album.Name, s.Album.GUID
		if out.Cover == "" {
			out.Cover = s.Album.CoverID
		}
	}
	return out
}

// libraryOf names the shared folder a song lies in: "/vol1/1000/音乐/…" is 音乐.
func libraryOf(p string) string {
	f := strings.Split(strings.Trim(p, "/"), "/")
	if len(f) > 3 && strings.HasPrefix(f[0], "vol") {
		return f[2]
	}
	return "飞牛音乐"
}
