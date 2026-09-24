# ext.to → Telegram 转发器

自动抓取 [ext.to](https://ext.to) 最新发布的种子，按分类与关键词过滤后推送到 Telegram 频道 / 群组 / 论坛话题。

内置中文 Web 管理面板（左侧导航），可在线调整全部配置、查看转发历史与实时日志。单个 Go 二进制，Docker 一键部署。

## 功能

- **定时抓取最新发布** — 按分类（电影 / 剧集 / 音乐 / 游戏 / 应用 / 图书 / 动漫 / 其他）与时间窗口（24 小时 ~ 1 个月）轮询
- **关键词过滤** — 包含 / 排除正则表达式，以及最小 / 最大体积限制
- **完整推送内容** — 标题、分类、体积、文件数、做种数、下载数、发布时间、详情页链接、**magnet 磁力链接**、**原始海报图**
- **TMDB 匹配** — 用详情页的 IMDb 编号或发布名首行标题匹配 TMDB，补全中文标题、评分、海报，支持「只推送匹配到 TMDB 的种子」
- **首次运行建立基线** — 不会把整站历史灌进你的频道，只从下一次扫描开始推送新帖
- **Web 管理面板（中文）** — 左侧导航分页：总览 / 转发记录 / 运行日志 / 抓取来源 / TMDB 匹配 / Telegram 推送 / 面板设置
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
4. 面板 → **Telegram 推送** → 填入 Token 与 Chat ID → 点右上角 **测试 Telegram** 验证

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
| `categories` | 抓取分类，可多选。`9` = 全部 |
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

### TMDB 匹配

在面板 **TMDB 匹配** 页面填入 API Key（免费申请：<https://www.themoviedb.org/settings/api>）即可启用。匹配策略：

1. **优先用编号**：ext.to 详情页会给出 `IMDb link`，转发器把它通过 TMDB 的 `/find/{id}` 转成 TMDB 条目。编号是权威来源，命中后不再校验标题。
2. **其次按标题**：没有编号时，把发布名交给解析器取出标题、年份、类型，再调 `/search/movie` 或 `/search/tv`。**只有标题完全一致（忽略大小写与标点）才接受**，避免把 `Ring Ring` 错配成别的片子。
3. **类型判定**：优先读发布名开头的 `[剧集]` / `[电影]` 前缀；没有前缀则由 `S01E02` 这类集数标记判定为剧集，带年份的判定为电影。

标题解析只读取**发布名本身**，不会读取简介、分享、字幕、体积、直达链接、标签等正文内容 —— 那些字段是噪声，只会让模糊匹配变差。

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
internal/telegram/   Bot API 最小实现（sendMessage / sendPhoto / getMe）
internal/forwarder/  调度核心：过滤、去重、推送、运行报告
internal/store/      转发历史持久化（JSON）
internal/web/        HTTP 接口与内嵌管理面板
deploy/              部署用 docker-compose.yml
```

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

## 常见问题

**日志里出现 `cloudflare challenge returned`**
Cookie 失效了：可能过期、出口 IP 变了，或 User-Agent 与获取 Cookie 时不一致。回到「配置 ext.to 访问」重新获取。

**Test scrape 正常，但频道收不到消息**
依次检查：Bot 是否已加入目标频道 / 群组并具有发言权限、Chat ID 是否为正负号正确、论坛群组是否填了正确的 topic ID。用 **测试 Telegram** 能立刻区分是 Bot 配置问题还是抓取问题。

**勾选了「只推送匹配到 TMDB 的种子」，但一条都没推送**
先点 **测试 TMDB** 确认 Key 有效；再到 **标题解析测试** 粘贴实际报错的发布名，看是否解析出了正确的标题与年份。若发布名本身不含年份（例如部分音乐、体育资源），标题搜索的命中率会明显下降，此时可改用 `{tmdb_...}` 变量之外的方式，或取消该勾选。日志里会记录 `skipped ... no tmdb match`。

**频道里的标题是其他语言**
`tmdb_lang` 决定返回哪种语言的标题与简介，默认 `zh-CN`。改成 `en-US` 可拿到英文原名，`{tmdb_original_title}` 则始终是原始语言标题。

**推送太频繁或太少**
调大 `interval_seconds` 降低频率；想收窄内容则用 `include` / `exclude` 或体积范围。`batch_size` 只限制单次扫描上限，不改变实际抓到的新帖数量。

**想重新开始（清空历史）**
**转发记录** 页面右上角 **清空记录**。清空后下一次扫描会重新建立基线，因此不会补推积压内容。

**忘记面板密码**
删除数据目录里的 `config.json`（或其中的 `admin_user` / `admin_password`），重启容器即恢复 `admin` / `admin` —— 注意这会一并重置其它设置。

## 免责声明

本项目仅用于自动化转发公开的种子索引页面信息，不存储、不代理任何受版权保护的内容。使用者需自行确保其使用方式符合所在地法律法规以及 ext.to 与 Telegram 的服务条款。
