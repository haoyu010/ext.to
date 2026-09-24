package config

// DefaultCategoryRules is the rule set a fresh install starts with.
//
// Order matters. Rules are tried most specific first, and rules that set the
// same number of conditions keep the order written here:
//
//   - 国漫 constrains both genre and origin, so it is matched before 国产剧 and
//     a Chinese cartoon is never filed as a plain drama.
//   - 纪录片 / 综艺 / 儿童 share a single condition with the regional rules, so
//     they are listed first and a US documentary becomes 纪录片 instead of
//     欧美剧.
//
// The last rule in each section has no conditions and catches everything else,
// so a release is never left without a category.
const DefaultCategoryRules = `# TMDB 分类规则
#
# 字段说明：
#   genre_ids            TMDB 类型编号，逗号分隔
#   original_language    TMDB 原始语言
#   origin_country       剧集的国家/地区（电影会自动改用 production_countries）
#   production_countries 电影的国家/地区
#   release_year         年份或年份区间，例如 2020-2025
#
# 同一个分类里的多个字段是「并且」；同一字段的多个值用逗号分隔是「或者」；
# 值前面加 ! 表示排除。
# 不带任何条件的分类是兜底，只有前面全部未命中时才会使用。
#
# 匹配顺序：条件多的优先；条件数相同时按下面书写的顺序。

movie:
  动画电影:
    genre_ids: '16'
  华语电影:
    original_language: 'zh,cn,bo,za'
  日韩电影:
    original_language: 'ja,ko'
  欧美电影:
    original_language: 'en,fr,de,es,it,pt,nl,ru'
  其他电影:

tv:
  国漫:
    genre_ids: '16'
    origin_country: 'CN,TW,HK'
  日番:
    genre_ids: '16'
    origin_country: 'JP'
  纪录片:
    genre_ids: '99'
  儿童:
    genre_ids: '10762'
  综艺:
    genre_ids: '10764,10767'
  国产剧:
    origin_country: 'CN,TW,HK'
  欧美剧:
    origin_country: 'US,FR,GB,DE,ES,IT,NL,PT,RU,UK'
  日韩剧:
    origin_country: 'JP,KP,KR,TH,IN,SG'
  未分类:
`

// DefaultCategoryBlacklist is the exclusion list a fresh install starts with.
//
// These are exactly the categories that were not asked for. What remains after
// excluding them is 国漫 and 国产剧, which is how a "Chinese animation and
// Chinese TV only" channel is expressed with the rules above.
var DefaultCategoryBlacklist = []string{"欧美剧", "日韩剧", "综艺", "纪录片", "日番", "未分类"}

// DefaultGenreBlacklist drops TMDB genres that are unwanted regardless of
// region.
var DefaultGenreBlacklist = []string{"真人秀"}

// DefaultGenreIDBlacklist is the numeric form of the same exclusion. Ids are
// used alongside the names because an id does not change when TMDB
// retranslates a genre: 99 is 纪录片 and 10764 is 真人秀.
var DefaultGenreIDBlacklist = []int{99, 10764}

// KnownGenreIDs lists the TMDB genre ids worth naming in the panel, in the
// Chinese names TMDB returns for zh-CN. The list is documentation rather than
// validation: the blacklist accepts any positive id, and this only exists
// because "10764" means nothing to an operator while "真人秀" does.
//
// Movie and TV ids overlap for the genres that exist in both (18 is 剧情 in
// both, 99 is 纪录片 in both), so the two are merged into one table.
var KnownGenreIDs = []struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}{
	{28, "动作"},
	{12, "冒险"},
	{16, "动画"},
	{35, "喜剧"},
	{80, "犯罪"},
	{99, "纪录片"},
	{18, "剧情"},
	{10751, "家庭"},
	{14, "奇幻"},
	{36, "历史"},
	{27, "恐怖"},
	{10402, "音乐"},
	{9648, "悬疑"},
	{10749, "爱情"},
	{878, "科幻"},
	{10770, "电视电影"},
	{53, "惊悚"},
	{10752, "战争"},
	{37, "西部"},
	{10759, "动作冒险"},
	{10762, "儿童"},
	{10763, "新闻"},
	{10764, "真人秀"},
	{10765, "科幻奇幻"},
	{10766, "肥皂剧"},
	{10767, "脱口秀"},
	{10768, "战争政治"},
}
