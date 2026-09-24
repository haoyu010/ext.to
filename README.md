# ext.to → Telegram 转发器

自动抓取 [ext.to](https://ext.to) 最新发布的种子，按分类与关键词过滤后推送到 Telegram 频道 / 群组 / 论坛话题。

内置中文 Web 管理面板（左侧导航），可在线调整全部配置、查看转发历史与实时日志。单个 Go 二进制，Docker 一键部署。

## 功能

- **定时抓取最新发布** — 按分类（电影 / 剧集 / 音乐 / 游戏 / 应用 / 图书 / 动漫 / 其他）与时间窗口（24 小时 ~ 1 个月）轮询
- **关键词过滤** — 包含 / 排除正则表达式，以及最小 / 最大体积限制
- **完整推送内容** — 标题、分类、体积、文件数、做种数、下载数、发布时间、详情页链接、**magnet 磁力链接**、**原始海报图**
- **TMDB 匹配** — 用详情页的 IMDb 编号或发布名首行标题匹配 TMDB，补全中文标题、评分、海报，支持「只推送匹配到 TMDB 的种子」。标题匹配会识别字幕组的多别名命名并核对 TMDB 别名，中文动画因此能匹配上
- **分类过滤（可按国别/类型收窄频道）** — 按 TMDB 元数据把条目归类为 国漫 / 日番 / 国产剧 / 欧美剧 / 日韩剧 / 综艺 / 纪录片 等，再用分类黑名单、类型名黑名单与 Genre ID 黑名单排除；默认配置即「只推送国漫与国产剧」
- **首次运行建立基线** — 不会把整站历史灌进你的频道，只从下一次扫描开始推送新帖
- **Web 管理面板（中文）** — 左侧导航分页：总览 / 转发记录 / 运行日志 / 抓取来源 / TMDB 匹配 / 分类过滤 / Telegram 推送 / 面板设置，每个分栏都有独立的「保存设置」
- **凭证打码** — 接口不会回传明文 Bot Token 与 Cookie
- **无需登录 ext.to** — 复用浏览器已通过 Cloudflare 校验的 `cf_clearance` Cookie

## 快速开始

### 1. 部署

```bash
mkdir -p /vol1/1000/docker/extto && cd /vol1/1000/docker/extto
curl -O https://raw.githubusercontent.com/haoyu010/ext.to/main/deploy/docker-compose.yml
docker compose up -d
```

打开 `http://<主机IP>:8090`，默认账号密码 **admin / admin**。

如果容器启动后日志报 `permission denied` 写不了 `/data`，把 `docker-compose.yml` 里的 `PUID` / `PGID` 改成该目录在宿主机上的属主（SSH 里执行 `id` 查看，飞牛用户通常是 `1000:1000`）。

### 2. 配置 Telegram

1. 在 Telegram 里找 [@BotFather](https://t.me/BotFather)，发送 `/newbot`，拿到 **Bot Token**（形如 `123456789:AAF-xxxxxxxx`）
2. 把 Bot 拉进你的频道 / 群组，并设为**管理员**（频道必须给发帖权限）
3. 拿到 **Chat ID**：
   - 频道：转发任意一条频道消息给 [@userinfobot](https://t.me/userinfobot)，或把频道设为公开后用 `@频道名`
   - 群组：直接填负数 ID，形如 `-1001234567890`
4. 面板 → **Telegram 推送** → 填入 Token 与 Chat ID → 点 **解析频道** 核对
5. 确认无误后 **保存设置**，再点右上角 **测试 Telegram** 发一条测试消息

**解析频道** 可以直接吃下 `@频道名`、`t.me/频道名`、`t.me/c/123456789/45` 这类链接，
从链接里换算出 `-100` 开头的真实 ID，并顺便告诉机器人当前是不是管理员、有没有发帖权限。
所以你不必再去手工推导 ID —— 拿不准就填你在 Telegram 里看到的那串东西，点一下即可。

### 3. 配置 ext.to 访问

ext.to 由 Cloudflare 保护，机房 IP 会被拦截。本项目复用你本机浏览器已通过的验证 Cookie：

1. **在浏览器里打开 ext.to**（必须和 Docker 主机**同一个出口 IP**，同一局域网通常就满足），通过 "Verifying you are human" 校验
2. 按 `F12` → **Application / 应用程序** → **Cookies** → `https://ext.to`
3. 复制 **`cf_clearance`** 的 **Value**（只复制值，不要整行）
4. 顺手复制 **`PHPSESSID`** 的 Value（可选，但建议填）
5. 面板 → **抓取来源 → 访问凭据**，粘贴进去
6. **User-Agent 保持与你浏览器一致** —— Cloudflare 把 Cookie 同时绑定 IP 和 UA，UA 不匹配会立刻失效

> `cf_clearance` 有效期通常为一年，但只要出口公网 IP 变了（运营商重播、换网络、开代理）就需要重新获取。

### 4. 开始转发

1. 面板 → **抓取来源** → 点右上角 **测试抓取**，确认能读到数据、过滤条件符合预期
2. 选好分类与时间窗口 → **保存设置**
3. 勾选 **开启监听** 再保存，或回到总览直接点 **启动监听**

首次扫描只会建立基线（不推送），这是刻意设计 —— 否则频道会被几百条历史帖子刷屏。

## 配置说明

| 配置项 | 说明 |
| --- | --- |
| `categories` | 抓取分类，可多选。`9` = 全部。默认 `2,7`（剧集 + 动漫），因为国漫发布在「动漫」分类下 |
| `age` | 时间窗口：`0` 24 小时、`1` 3 天、`2` 7 天、`3` 14 天、`4` 1 个月 |
| `max_pages` | 每个分类抓取页数，每页 50 条 |
| `include` / `exclude` | 正则，每行一条，忽略大小写。含 `include` 时标题必须命中至少一条 |
| `min_size_mb` / `max_size_mb` | 体积范围，`0` 为不限制。体积未知的条目不会被过滤掉 |
| `batch_size` | 单次扫描最多推送条数，防止停机后补推暴增 |
| `interval_seconds` | 轮询间隔，最小 60 秒 |
| `message_topic` | 论坛群组的主题 ID，填数字或 `https://t.me/c/xxx/42` 链接均可 |
| `with_poster` | 下载海报并以图片消息发送（不加此选项时为纯文本） |
| `with_magnet` | 解析签名 magnet 链接。关闭可省去每条种子的额外请求 |
| `silent` | 静默推送，不响铃 |
| `disable_web_preview` | 关闭链接预览，让帖子更紧凑 |
| `proxy` | HTTP 代理，仅在直连不通或需固定出口 IP 时使用 |
| `tmdb_key` | TMDB API Key（v3 密钥或 v4 令牌）。留空则完全不启用 TMDB |
| `tmdb_lang` | TMDB 语言，默认 `zh-CN`，返回中文标题与简介 |
| `tmdb_only` | 只推送匹配到 TMDB 的种子，未匹配的计入「跳过」而不是失败 |
| `poster_source` | 海报来源：`auto`（优先 TMDB，失败回退种子站）/ `tmdb` / `tracker` |
| `category_rules` | 分类规则（YAML），按 TMDB 元数据归类。留空则不分类，下面的分类黑名单也不会有作用 |
| `category_blacklist` | 分类黑名单，一行一个，填规则里的分类名 |
| `genre_blacklist` | TMDB 类型名黑名单，例如 `真人秀`，忽略大小写 |
| `genre_id_blacklist` | TMDB 类型编号黑名单，例如 `99`（纪录片）、`10764`（真人秀） |

### 分类过滤

种子站自己的分类太粗：`动漫` 一类里同时有国漫、日番和欧美动画，**单靠站点分类无法只留国漫**。所以本项目按 **TMDB 匹配结果的元数据** 分类，再用黑名单排除不要的分类。

面板 **分类过滤** 页面可以编辑规则与三份黑名单，**恢复默认规则** 按钮会把内置规则填回输入框（记得再点 **保存设置**）。默认值就是「只推送国漫与国产剧」：规则会先判 `国漫`（动画 + 中国台港），再判 `国产剧`。

默认黑名单要排除**其余所有分类**，漏掉一个就等于放行一个：`欧美剧 / 日韩剧 / 综艺 / 纪录片 / 日番 / 儿童 / 未分类`，加上全部电影分类（`动画电影 / 华语电影 / 日韩电影 / 欧美电影 / 其他电影`）。`儿童` 尤其容易漏——B 站来源的条目常常只有「儿童」这一个类型、国别又没有 TMDB 能识别的值，漏了就会混进来。

抓取分类的默认值同样是 `剧集 + 动漫`：国漫发在「动漫」分类下，只勾「剧集」会一条国漫都收不到。

规则文件的写法：

```yaml
tv:
  国漫:                      # 二级就是分类名，会出现在面板与 {category_tmdb}
    genre_ids: '16'          # TMDB 类型编号，逗号分隔
    origin_country: 'CN,TW,HK'
  日番:
    genre_ids: '16'
    origin_country: 'JP'
  未分类:                     # 没有任何条件的分类是兜底
```

可用字段：`genre_ids`、`original_language`、`origin_country`（剧集）、`production_countries`（电影）、`release_year`（支持 `2020-2025`）。同一分类内多个字段是「并且」，同一字段多个值用逗号分隔是「或者」，值前加 `!` 表示排除。**条件多的分类优先**，条件数相同时按书写顺序，所以 `国漫` 写在 `国产剧` 前面时，国产动画不会被误判成普通剧集。

两点必须清楚：

- **分类依赖 TMDB**，没有填 TMDB API Key 时规则不会生效。
- **匹配不到 TMDB 的条目会被跳过**，因为它可能属于任何分类，无法确认就不推送；这类跳过与「命中黑名单」在日志里有不同措辞。

转发记录页会显示每个条目的判定分类，跳过时也能看到它被归到了哪一类。

### TMDB 匹配

在面板 **TMDB 匹配** 页面填入 API Key（免费申请：<https://www.themoviedb.org/settings/api>）即可启用。匹配策略：

输入框里的 Key 会被 **测试 TMDB** 与「标题解析」直接采用，可以先验证再点 **保存设置**；
保存时若输入框仍是掩码（`abcd********wxyz`）或留空，都会保留已存的值，不会被掩码覆盖。

1. **优先用编号**：ext.to 详情页会给出 `IMDb link`，转发器把它通过 TMDB 的 `/find/{id}` 转成 TMDB 条目。编号是权威来源，命中后不再校验标题。
2. **其次按标题**：没有编号时，把发布名交给解析器取出标题、年份、类型，再调 `/search/movie` 或 `/search/tv`。接受条件分两步：

   1. **条目自身的名字**与查询完全一致（忽略大小写与标点），避免把 `Ring Ring` 错配成别的片子。
   2. 都不一致时，才用 **TMDB 别名**（`alternative_titles`）比对，同样要求完全一致。

   第二步是国漫能不能匹配上的关键：字幕组发布的 `雪王来了`、`Kimi ga Shinu made Koi wo Shitai`，在 TMDB 上分别叫 `雪王驾到`、`与你相恋到生命尽头`，只有别名对得上。别名属于较弱证据（置信度 0.8，低于标题命中的 0.9），且**只在前一步完全落空时才启用**，查询本身太短（纯拉丁字母少于 3 个）时也不启用 —— 否则 `Re` 这种两字母查询会命中任何一个恰好叫这个名字的条目。
3. **类型判定**：优先读发布名开头的 `[剧集]` / `[电影]` 前缀；没有前缀则由 `S01E02`、`第14话` 或分离的集数（`名称 - 24`）判定为剧集，带年份的判定为电影。

标题解析只读取**发布名本身**，不会读取简介、分享、字幕、体积、直达链接、标签等正文内容 —— 那些字段是噪声，只会让模糊匹配变差。

一个发布名会派生多个候选再逐个尝试，因为字幕组的命名方式不止一种：

- `[Shridhuu][1080p] GuAn / 一斩苍穹 / Yi Zhan Cangqiong` → 斜杠分隔的每个别名各试一次
- `【字幕组】碧蓝之海3 Grand Blue Dreaming!` → 中文名与拉丁名分开试（整串匹配不上任何条目）
- `[BDMV][251008][桃源暗鬼 / Tougen Anki][BDMV]` → 标题在方括号里，就取方括号内容
- `幼女战记II` → 去掉续作标记后试 `幼女战记`
- `一人之下 第六季` → 用基础名 `一人之下` 去搜（**不是**先搜带季的全名）

方括号里的内容一律算元数据，**不论它在开头还是结尾**，所以 `[字幕组] 名称 [01-12][1080p]`
会先剥掉首尾的括号再搜。剥完只剩发布者尾巴的情况（`[BDMV][…][JPN]-YE` 只剩 `-YE`）不会拿去搜 ——
那不是标题，而 TMDB 里恰好有一部叫 `Ye!` 的电影。

### 季数：搜基础名，转发时带季数

TMDB 把**一部剧集做成一条条目，覆盖它的全部季**。所以 `一人之下 第六季` 这种发布名直接搜是搜不到的
（TMDB 没有叫这个名字的条目），必须用基础名 `一人之下` 去搜。本项目就是这么做的：**带季数的名字根本不会发出去搜**，
省掉一次注定失败的请求。

但季数不能丢，转发时会带回来：

- `{season}` / `{episode}` —— 季数、集数，取自发布名；电影或没写季数的发布名为空
- `{season_label}` —— 渲染成 ` 第 6 季`（含前导空格），直接接在标题后面即可

内置的 **TMDB 模板**已经用上了，效果是 `<b>一人之下</b> 第 6 季 (2016)`；电影则渲染成 `<b>流浪地球2</b> (2023)`，不会多出空格。

**为什么只去「第 N 季」而不去裸数字**：TMDB 对电影是**每部一条条目**，`流浪地球`、`流浪地球2`、`流浪地球3` 是三条。
把 `流浪地球2` 的数字去掉再搜，会把续集匹配到第一部。所以只有「第 N 季 / 第 N 部 / 第 N 期」这种明确的季标记才会被拆开，
裸数字、罗马数字（`Clevatess II`）保持原样。

拆开的季数还会用来**挑对续作**：TMDB 里有一类条目把季写进了正式名（`毛骗 第二季` 和 `毛骗` 是两条），
搜基础名时两条都会返回，此时优先选名字里带该标记的那条。没有这个判断，`毛骗 第二季` 会被匹配到第一季。

候选会去重，且**只有前一个失败才会试下一个**，所以能直接命中的发布名不会多花请求。

匹配成功后可用 `{tmdb_title}`、`{tmdb_rating}` 等变量；未匹配到时会自动回退为种子标题，不会留下空占位。TMDB 不可达时只记录日志，推送照常进行。

面板里的 **标题解析测试** 可以粘贴任意发布名，查看解析结果与匹配到的条目，用来验证规则是否符合预期。

### 推送模板

模板使用 Telegram HTML（支持 `<b>` `<i>` `<a href>` `<code>` `<pre>`），可用变量：

| 变量 | 内容 |
| --- | --- |
| `{title}` | 种子标题 |
| `{tmdb_title}` | TMDB 匹配标题，未匹配时为种子标题 |
| `{tmdb_original_title}` | TMDB 原始语言标题 |
| `{tmdb_year}` `{tmdb_rating}` `{tmdb_votes}` | 年份、评分、评分人数 |
| `{tmdb_url}` `{tmdb_id}` `{tmdb_type}` | TMDB 链接、编号、类型（movie / tv） |
| `{tmdb_overview}` | TMDB 简介，按 Telegram 限制截断为 320 字 |
| `{category_tmdb}` | 分类规则判定的分类，如 `国漫`、`国产剧`；未命中时为「未分类」，无规则时回退为站点分类 |
| `{category}` | 分类路径，如 `Movies - Highres Movies` |
| `{size}` `{files}` | 体积、文件数 |
| `{seeds}` `{leeches}` | 做种数、下载数 |
| `{age}` | 发布时间，如 `5 minutes ago` |
| `{source}` `{uploader}` | 来源站点、发布者 |
| `{url}` `{magnet}` `{id}` | 详情页链接、磁力链接、种子 ID |

默认模板：

```html
<b>{title}</b>

📁 {category}
💾 {size} · 📄 {files} files
🌱 {seeds} seeders · {leeches} leechers
🕐 {age}

<a href="{url}">Open on ext.to</a>
```

如果标题里包含非法 HTML 标签，转发器会自动降级为纯文本重发一次，不会因为单条标题导致整个流程中断。

TMDB 模板（面板里点 **TMDB 模板** 一键套用）：

```html
<b>{tmdb_title}</b> ({tmdb_year})
⭐ {tmdb_rating}/10 · {tmdb_votes} votes

📁 {category}
💾 {size} · 📄 {files} files
🌱 {seeds} seeders · {leeches} leechers
🕐 {age}

<a href="{tmdb_url}">TMDB</a> · <a href="{url}">ext.to</a>
```

## 本地开发

需要 Go 1.26+。

```bash
go run ./cmd/server -data ./data -addr :8080
go test ./...
```

跑通需要网络与有效 Cookie 的真实链路测试：

```bash
LIVE_COOKIE_FILE=./cookie.json go test ./internal/scrape/ -run TestLiveMagnetAndPoster -v
```

`cookie.json` 是抓取到的 Cookie 结果（见下方数据结构），该测试会真实访问 ext.to。

### 目录结构

```
cmd/server/          程序入口：启动 HTTP 服务与轮询循环
internal/config/     配置读写、校验、打码，以及推送模板渲染
internal/media/      发布名解析：标题去噪、年份、季集、[剧集] / [电影] 前缀
internal/scrape/     ext.to 抓取：列表解析、magnet 签名、海报提取
internal/tmdb/       TMDB 匹配：IMDb 编号换条目、标题精确搜索
internal/rules/      分类规则引擎：YAML 规则解析、元数据匹配、黑名单判定
internal/telegram/   Bot API 最小实现（sendMessage / sendPhoto / getMe）
internal/forwarder/  调度核心：过滤、去重、推送、运行报告
internal/store/      转发历史持久化（JSON）
internal/web/        HTTP 接口与内嵌管理面板
deploy/              部署用 docker-compose.yml
scripts/             版本号递增脚本
```

## 版本号

版本号的唯一来源是仓库根目录的 `VERSION`（`MAJOR.MINOR.PATCH`）。CI 读取它并同时用于三处，因此镜像标签、二进制内嵌版本与面板页脚永远一致：Docker 镜像标签（`1.2.3`、`1.2`、`latest`、`sha`）、`-ldflags -X main.version`、以及 `/api/health` 返回的版本。面板侧边栏页脚与 **面板设置 → 关于** 显示的就是这个值。

改动后递增版本号：

```bash
./scripts/bump-version.sh patch   # 1.0.0 -> 1.0.1
./scripts/bump-version.sh minor   # 1.0.1 -> 1.1.0
./scripts/bump-version.sh major   # 1.1.0 -> 2.0.0
```

脚本只改 `VERSION`；工作区干净时它会一并提交并打 `vX.Y.Z` tag。推送 tag 会触发 CI 构建该版本的镜像。想看当前二进制版本可直接运行 `extto -version`。

## 技术要点

抓取链路有两处非标准实现，都在 `internal/scrape`：

**magnet 链接需要签名。** ext.to 不直接暴露磁力链接，而是通过 `POST /ajax/getTorrentMagnet.php` 下发，请求需要
`hmac = SHA256("<torrent_id>|<timestamp>|<pageToken>")`，其中 `pageToken` 是**每个种子详情页独有的**，且
`hmac` 与 `torrent_id` 绑定。因此每条种子都要先取详情页拿 token，跨种子复用会返回 `Invalid security token`。

**列表页 HTML 顺序不固定。** 标题链接的 `href` 出现在 `class` 属性之前，用正则匹配容易失效，所以解析基于
`golang.org/x/net/html` 构建 DOM 树，测试用例直接使用真实页面快照（`internal/scrape/testdata`）。

**详情页只请求一次。** 海报、IMDb 编号和 magnet 的签名 token 都在同一个详情页上，而 magnet 的 `pageToken`
绑定到具体的一次页面加载。转发器因此一次性取回页面，再从同一份 body 里分别解析三项数据，避免重复请求，
也保证 token 与正在推送的种子严格对应。

**电影页与剧集页结构不同。** 电影页用 `Movie:` 行给出正式片名，海报放在 `detail-torrent-image`；
剧集页没有对应行，而是以元数据块首行的 `Original name:` 标识，且 `detail-torrent-image` 指向
`/static/img/no-torrent-image.png` 占位图 —— 真正的海报在 `serial_poster` 块里，是 TMDB 的图片地址。
解析器按「无 `Movie:` 且有 `Original name:`」判定为剧集，并主动跳过占位图，否则会把种子站的灰底占位图
当海报发出去。

**剧集年份不可直接用作筛选条件。** 种子名里的年份是该集的播出年份，通常晚于剧集首播年份；
TMDB 的 `year` / `first_air_date_year` 参数是精确匹配，用它查询会把正确结果过滤掉。因此标题搜索不带年份，
改由本地按媒体类型分别判断：电影允许 ±1 年误差（电影节首映与公映常跨年），剧集只要求首播年份不晚于种子年份。

**国漫发布名要按多种候选去搜索。** 国漫的发布名通常是
`[字幕组] 中文名 / Romaji / English - 第14话`，而且详情页既没有 IMDb 编号、也没有 `Movie:` / `Original name:` 这类正式标题行，
只能靠发布名。整串清洗后直接查 TMDB 基本查不到，所以 `media.SearchTitles` 会依次给出若干候选标题
（正式名、斜杠分隔的别名、破折号分段），逐个尝试，命中即停，因此能匹配上的条目不会多花请求。

其中纯数字片段**绝不能**作为候选：TMDB 里真有叫 `12`、`86` 的作品，拿集数去搜会把一条国漫匹配到毫不相干的剧集。
错误匹配比匹配不上更糟 —— 它决定分类，也就决定了这条会不会被推出去。

## 常见问题

**日志里出现「遇到 Cloudflare 验证」**
Cookie 失效了：可能过期、出口 IP 变了，或 User-Agent 与获取 Cookie 时不一致。回到「配置 ext.to 访问」重新获取。

**Test scrape 正常，但频道收不到消息**
先点 **测试 Telegram**：它按顺序检查 Token、能否读到该频道、Bot 是不是管理员、有没有「发布消息」权限，
哪一步失败就直说哪一步，不需要自己逐项猜。剩下的可能性只有论坛群组的 topic ID 填错。

**填了 TMDB API Key，点「解析」却提示未配置**
Key 必须点 **保存设置** 才会写盘。不过「测试 TMDB」和「解析」都会直接使用输入框里的值，
所以可以先填、先验证，确认能用再保存。若提示「TMDB 拒绝了该 API Key（401）」，
说明是 Key 本身无效（复制不全、已重置、带了多余空格），而不是没保存。

**勾选了「只推送匹配到 TMDB 的种子」，但一条都没推送**
先点 **测试 TMDB** 确认 Key 有效；再到 **标题解析测试** 粘贴实际报错的发布名，看是否解析出了正确的标题与年份。若发布名本身不含年份（例如部分音乐、体育资源），标题搜索的命中率会明显下降，此时可改用 `{tmdb_...}` 变量之外的方式，或取消该勾选。日志里会记录「没有匹配到 TMDB 条目」。

**频道里的标题是其他语言**
`tmdb_lang` 决定返回哪种语言的标题与简介，默认 `zh-CN`。改成 `en-US` 可拿到英文原名，`{tmdb_original_title}` 则始终是原始语言标题。

**剧集匹配不到，或匹配成了别的剧**
剧集页的 `Original name` 往往是日文、韩文原名，TMDB 不一定收录。转发器会先按正式名搜索，再退回种子名里的英文标题，
两轮都失败才判定未匹配。若仍匹配错，多半是重名剧集，可用 `tmdb_lang` 调整后再到 **标题解析测试** 复核。

**推送太频繁或太少**
调大 `interval_seconds` 降低频率；想收窄内容则用 `include` / `exclude` 或体积范围。`batch_size` 只限制单次扫描上限，不改变实际抓到的新帖数量。

**想重新开始（清空历史）**
**转发记录** 页面右上角 **清空记录**。清空后下一次扫描会重新建立基线，因此不会补推积压内容。

**忘记面板密码**
删除数据目录里的 `config.json`（或其中的 `admin_user` / `admin_password`），重启容器即恢复 `admin` / `admin` —— 注意这会一并重置其它设置。

## 免责声明

本项目仅用于自动化转发公开的种子索引页面信息，不存储、不代理任何受版权保护的内容。使用者需自行确保其使用方式符合所在地法律法规以及 ext.to 与 Telegram 的服务条款。
