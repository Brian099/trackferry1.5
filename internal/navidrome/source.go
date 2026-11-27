package navidrome

import (
	"context"
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

// Songs pages the enumerated library. page 1 triggers the full walk and caches it; later
// pages slice the cache.
func (m *Manager) Songs(ctx context.Context, page, size int) ([]library.Song, int, error) {
	if page == 1 {
		s, err := m.session()
		if err != nil {
			return nil, 0, err
		}
		all, err := m.enumerate(ctx, s)
		if err != nil {
			return nil, 0, err
		}
		m.mu.Lock()
		m.cache = all
		m.mu.Unlock()
	}
	m.mu.RLock()
	all := m.cache
	m.mu.RUnlock()
	total := len(all)
	start := (page - 1) * size
	if start >= total {
		return nil, total, nil
	}
	end := min(start+size, total)
	return all[start:end], total, nil
}

// toLibrarySong converts one Navidrome song; ok is false for what speakers cannot play.
func toLibrarySong(s song) (library.Song, bool) {
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
