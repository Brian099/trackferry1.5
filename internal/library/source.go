package library

import "context"

// Song is one track a remote library holds, in source-neutral form. The source has
// already dropped what speakers cannot play and normalised the format.
type Song struct {
	ID         string // the source's own id (becomes the track's GUID and Path suffix)
	Title      string
	Artist     string
	Album      string
	AlbumID    string
	Cover      string // the source's cover id, stored with the source's cover prefix
	DurationMS int64
	Format     string // mp3 | flac | m4a | aac | ogg | wav | alac
	Size       int64
	SampleRate int
	Bits       int
	Favorite   bool
	AddedMS    int64
	Root       string // the shared folder it lies in; "" = the source's label
}

// Source is a library that lists its own songs and is mirrored into the index.
type Source interface {
	ID() string          // "feiniu" | "synology" | "navidrome"
	Prefix() string      // Path prefix: "fn:" | "ds:" | "nd:"
	CoverPrefix() string // cover id prefix: "" | "ds-" | "nd-" (must stay within [\w-])
	Label() string       // shown to people
	Linked() bool        // a session exists (it may have expired)
	Songs(ctx context.Context, page, size int) ([]Song, int, error)
}

// Walker is a source that has no "page the whole library" call. It reports each song as it
// is read, so the sync progress is live; it returns the number of songs reported.
type Walker interface {
	Walk(ctx context.Context, add func(Song) error) (int, error)
}
