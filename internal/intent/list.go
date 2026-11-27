package intent

import (
	"regexp"
	"strings"
)

// What a list request means, in Control.
const (
	ListFavorites = "favorites"
	ListPlaylist  = "playlist" // Title is its name; "" when none was said
	ListAlbum     = "album"    // Title is its name, Artist whose; no Title: the one the song is from
)

// Verbs that only make a request of a list: "听我的收藏" is one, "听海" is not.
var listVerbs = []string{"循环播放", "连续播放", "顺序播放", "开始播放", "打开", "听听", "听一下", "听"}

const (
	listOwner = `^(?:一下)?(?:飞牛音乐|飞牛)?(?:里面|里|中)?的?(?:我的|我)?`
	listSongs = `(?:里面|里|中)?的?(?:所有|全部)?(?:歌曲|歌|音乐|单曲)?`
)

var (
	favoritesRe = regexp.MustCompile(listOwner + `(?:所有|全部)?的?(?:收藏(?:过的|夹|列表|歌单)?` + listSongs + `|(?:喜欢|红心)的?(?:歌曲|歌单|歌|音乐))$`)
	namedRe     = regexp.MustCompile(listOwner + `(?:名字叫|名叫|名为|叫做|叫)?(.*?)(?:的|这个|那个)?歌单` + listSongs + `$`)
	thisAlbumRe = regexp.MustCompile(`^(?:一下)?(?:这首歌|这首|这歌|当前歌曲|现在这首歌|现在这首)?(?:所在|所属)?的?(?:这张|这个|整张|当前|当前这张|本张)?(?:完整|全部|整张)?专辑(?:里面|里|中)?的?(?:所有|全部)?(?:歌曲|歌)?$`)
	albumRe     = regexp.MustCompile(`^(?:一下)?(?:整张|一张|整个|全部)?(.+?)(?:的)?(?:这张|那张|整张|这个)?专辑` + listSongs + `$`)
	albumsRe    = regexp.MustCompile(`^(?:一下)?(?:(.+?)的)?(?:这张|那张|整张)?专辑(?:名字叫|名叫|名为|叫做|叫|里的|里)?(.+)$`)
	afterRe     = regexp.MustCompile(listOwner + `歌单(?:名字叫|名叫|名为|叫做|叫)?(.+)$`)
)

// list recognises "播放我的收藏", "播放测试歌单", "随机播放歌单测试".
func list(q string) (Intent, bool) {
	body, ok := "", false
	for _, v := range listVerbs {
		if body, ok = strings.CutPrefix(q, v); ok {
			break
		}
	}
	if !ok {
		if body, ok = stripVerb(q); !ok {
			return Intent{}, false
		}
	}
	body = strings.TrimSpace(body)
	for _, p := range []string{"随机播放", "播放", "随机"} {
		body = strings.TrimPrefix(body, p)
	}
	tight := strings.ReplaceAll(body, " ", "")
	if favoritesRe.MatchString(tight) {
		return Intent{Kind: KindList, Control: ListFavorites}, true
	}
	for _, re := range []*regexp.Regexp{namedRe, afterRe} {
		if m := re.FindStringSubmatch(body); m != nil {
			return Intent{Kind: KindList, Control: ListPlaylist, Title: strings.TrimSpace(m[1])}, true
		}
	}
	return album(body)
}

// album recognises "播放专辑范特西", "播放周杰伦的范特西专辑", "播放这张专辑". "周杰伦的专辑" is about
// a singer as far as words tell; the library knows better (routing).
func album(body string) (Intent, bool) {
	tight := strings.ReplaceAll(body, " ", "")
	if thisAlbumRe.MatchString(tight) {
		return Intent{Kind: KindList, Control: ListAlbum, Follow: true}, true
	}
	if strings.HasSuffix(tight, "的专辑") && strings.Count(tight, "的") == 1 {
		return Intent{}, false
	}
	whose, name := "", ""
	if m := albumsRe.FindStringSubmatch(body); m != nil && !albumTail(m[2]) {
		whose, name = m[1], m[2]
	} else if m := albumRe.FindStringSubmatch(body); m != nil {
		name = m[1]
		if a, t, ok := strings.Cut(name, "的"); ok && a != "" && t != "" {
			whose, name = a, t
		}
	}
	if name = cleanName(name); name == "" {
		return Intent{}, false
	}
	return Intent{Kind: KindList, Control: ListAlbum, Title: name, Artist: cleanName(whose)}, true
}

// albumTail reports words after 专辑 that name no album.
func albumTail(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range []string{"里面", "里", "中", "的", "所有", "全部"} {
		s = strings.TrimPrefix(s, p)
	}
	switch s {
	case "", "歌", "歌曲", "音乐", "单曲":
		return true
	}
	return false
}
