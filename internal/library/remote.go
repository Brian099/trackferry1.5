package library

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"trackferry/internal/store"
)

const pageSize = 200

// syncSource mirrors one source's index. Only that source's tracks are removed, so a
// source that fails or goes away never empties the others.
func (l *Library) syncSource(ctx context.Context, id int64, rep *Report, src Source) error {
	known := map[string]store.TrackRow{}
	l.mu.RLock()
	for _, t := range l.idx.rows {
		if strings.HasPrefix(t.Path, src.Prefix()) {
			known[t.Path] = t
		}
	}
	l.mu.RUnlock()

	roots := map[string]int{}
	got, total := 0, 0
	seen := make(map[string]bool, len(known))
	var changed []store.TrackRow
	flush := func() error {
		if len(changed) == 0 {
			return nil
		}
		err := l.st.SaveTracks(ctx, id, changed, nil)
		changed = changed[:0]
		return err
	}
	add := func(s Song) error {
		l.mu.Lock()
		got++
		l.seen++
		t, ok := songRow(src, s)
		if !ok {
			rep.Skipped++
			l.mu.Unlock()
			return nil
		}
		i, ok := roots[t.Root]
		if !ok {
			i = len(rep.Roots)
			roots[t.Root] = i
			rep.Roots = append(rep.Roots, RootReport{Name: t.Root, Samples: []string{}})
		}
		rep.Roots[i].Tracks++
		old := known[t.Path]
		t.ID = old.ID
		seen[t.Path] = true
		if old.ID == 0 || old != t {
			changed = append(changed, t)
			rep.Added++
		}
		l.mu.Unlock()
		if len(changed) >= batch {
			return flush()
		}
		return nil
	}

	if w, ok := src.(Walker); ok {
		n, err := w.Walk(ctx, add)
		if err != nil {
			return err
		}
		total = n
	} else {
		for page := 1; ; page++ {
			songs, n, err := src.Songs(ctx, page, pageSize)
			if err != nil {
				return err
			}
			total = n
			for _, s := range songs {
				if err := add(s); err != nil {
					return err
				}
			}
			if len(songs) < pageSize || got >= total {
				break
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if got < total {
		return fmt.Errorf("%s 说有 %d 首，只读到 %d 首", src.Label(), total, got)
	}
	var gone []int64
	for p, t := range known {
		if !seen[p] {
			gone = append(gone, t.ID)
		}
	}
	var removed int64
	for part := range slices.Chunk(gone, 500) {
		if err := l.st.DropTracks(ctx, part); err != nil {
			return err
		}
		removed += int64(len(part))
	}
	if removed > 0 {
		l.mu.Lock()
		rep.Removed += removed
		l.mu.Unlock()
	}
	return nil
}

// songRow converts a source-neutral song into an indexed row; ok is false for what cannot
// be played on its own (unknown format, no id, no title). Sources already drop such songs,
// so this is only a safety net.
func songRow(src Source, s Song) (store.TrackRow, bool) {
	if s.Format == "" || s.ID == "" || strings.TrimSpace(s.Title) == "" {
		return store.TrackRow{}, false
	}
	root := s.Root
	if root == "" {
		root = src.Label()
	}
	return store.TrackRow{
		Path:       src.Prefix() + s.ID,
		GUID:       s.ID,
		Root:       root,
		Size:       s.Size,
		Title:      s.Title,
		Artist:     s.Artist,
		Album:      s.Album,
		AlbumID:    s.AlbumID,
		Cover:      src.CoverPrefix() + s.Cover,
		DurationMS: s.DurationMS,
		Format:     s.Format,
		SampleRate: s.SampleRate,
		Bits:       s.Bits,
		Tagged:     true,
		Favorite:   s.Favorite,
		AddedMS:    s.AddedMS,
	}, true
}

// SourceOf returns the source a track's path belongs to, or nil.
func (l *Library) SourceOf(path string) Source {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, src := range l.Sources {
		if strings.HasPrefix(path, src.Prefix()) {
			return src
		}
	}
	return nil
}
