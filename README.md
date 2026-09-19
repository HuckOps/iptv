# IPTV 直播源拨测工具

并发拨测多个直播源，过滤死链，并按**频道名 / 分组统一去重**（自动识别 `CCTV1`/`CCTV-1` 这类分隔符差异，以及简体/繁体实为同一台的情况），输出一份干净可用的直播列表与 JSON 报告。

## 编译

```bash
go build -o iptv .
```

需要 Go 1.27+。

## 用法

```bash
# 拨测默认源（global），输出 live.m3u + report.json
./iptv

# 指定区域：cn（中国合规）/ global（全球），默认 global
./iptv -region cn     -out live_cn.m3u     -report report_cn.json
./iptv -region global -out live_global.m3u -report report_global.json

# 拨测本地 / 自定义的 m3u 列表
./iptv -input mylist.m3u -out live.m3u

# 自定义源（覆盖默认区域设置）
./iptv -sources "https://a.m3u,https://b.m3u"
```

### 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-region` | `global` | 源区域：`cn` / `global` |
| `-sources` | 空 | 逗号分隔的 m3u URL，设置后覆盖 `-region` |
| `-input` | 空 | 本地 m3u 文件，设置后跳过网络拉取 |
| `-out` | `live.m3u` | 存活频道输出文件 |
| `-deadout` | 空 | 死亡频道输出文件（可选） |
| `-report` | `report.json` | JSON 报告路径 |
| `-workers` | `50` | 并发拨测数 |
| `-timeout` | `8` | 单请求超时（秒） |
| `-retries` | `1` | 网络错误 / 5xx 重试次数 |
| `-insecure` | `false` | 跳过 TLS 证书校验 |
| `-ua` | 内置 | 请求 User-Agent |
| `-verbose` | `false` | 每行日志额外打印频道 URL |
| `-dedup` | `true` | 按归一化频道名合并重复台（识别分隔符 / 简繁差异） |

运行时会逐频道打印拨测状态（存活/失败、状态码、延迟、分组、名称）到标准输出，便于在日志里直接查看每个源的可用性。

## 输出

- **`*.m3u`** —— 仅包含拨测存活、且去重统一后的频道，保留原始 `tvg-id`/`tvg-logo`/`group-title` 等元数据。
- **`*.json`** —— 报告，核心字段：
  - `total` / `alive` / `dead` / `unified_channels` / `duplicates_merged`
  - `by_group`：各分组总数与存活数
  - `channels`：全部最终频道的单一数组，每条带 `alive` 布尔、`status`、`latency_ms`、`group`、`uri`

## 去重与统一

同台多源会被合并为一条：
- 频道名归一化（繁→简、转小写、去分隔符）后相同即视为同一台；
- URL 取延迟最低的一个；
- 规范名优先保留带分隔符写法（`CCTV-1` 而非 `CCTV1`）；
- 分组取多数票并同样转简体统一。

可用 `-dedup=false` 关闭。

## 自动维护（GitHub Actions）

仓库内置两个独立任务，每 4 小时自动运行一次（错开半小时）：

- `probe-cn.yml` —— 产出 `live_cn.m3u` + `report_cn.json`
- `probe-global.yml` —— 产出 `live_global.m3u` + `report_global.json`

均支持手动触发（`workflow_dispatch`），结果自动提交回仓库。
