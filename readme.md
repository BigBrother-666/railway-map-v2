# Railway Map V2

BiliCraftTicketSystem 的网页线路图项目。仓库包含 Go 后端、React 前端，以及与 Minecraft 插件的 WebSocket 对接能力。

## 项目结构

- `backend/`：Go HTTP/WebSocket 服务，缓存插件同步的地图数据，广播实时列车，处理登录、购票和乘车历史。
- `frontend/`：React 18 + TypeScript + Vite 前端，负责 MapLibre 地图渲染、本地寻路（直达 + 联程票，语义与插件 `GeoRouteEngine` 对齐）、在线购票、实时列车和历史记录展示。
- `docs/`：设计与任务说明。
- `docker-compose.yml`：前后端容器化部署入口。

后端位于游戏插件和浏览器前端之间：插件通过 `/internal/plugin` 推送 `geojson`、铁路系统信息、线路信息、列车遥测、购票结果和网页登录绑定；前端通过 `/api/v1/*` 获取数据并通过 `/api/v1/realtime` 接收实时列车数据。

## 本地开发

后端：

```bash
cd backend
BCTS_DB_DRIVER=mysql
BCTS_DB_DSN='bcts:change-me-db-password@tcp(127.0.0.1:3306)/bcts_web?charset=utf8mb4&parseTime=true&loc=Local'
go build -o bcts-server ./cmd/server
./bcts-server -config config.yml
```

后端默认使用 MySQL 8.x。本地直接运行后端时，请先创建数据库和用户，或把 `BCTS_DB_DSN` 指向已有 MySQL。也可以在 `config.yml` 切换为 SQLite。

```bash
cd backend
go build -o bcts-server ./cmd/server
./bcts-server -config config.yml
```

前端：

```bash
cd frontend
npm install
npm run dev
```

插件 `config_map.yml` 示例：

```yaml
web-link:
  enabled: true
  backend-url: "ws://localhost:8080/internal/plugin"
  shared-token: "change-me"
```

`shared-token` 必须与后端 `plugin.sharedToken` 一致。

## 配置文件

运行配置从 `backend/config.yml` 读取。字符串值支持 `${ENV}` 环境变量展开。

常用环境变量：

- `BCTS_PLUGIN_TOKEN`：插件 WebSocket 共享密钥。
- `BCTS_JWT_SECRET`：网页登录会话 JWT 签名密钥。
- `BCTS_DB_DRIVER`：数据库类型，可选 `mysql` 或 `sqlite`；留空默认 `mysql`。
- `BCTS_DB_DSN`：MySQL 连接串，例如 `bcts:password@tcp(127.0.0.1:3306)/bcts_web?charset=utf8mb4&parseTime=true&loc=Local`。
- `BCTS_DB_PATH`：SQLite 数据库文件路径，例如 `data/bcts-web.db`。
- `MS_CLIENT_ID` / `MS_CLIENT_SECRET`：微软 OAuth 应用凭据。

### 后端配置

| 配置项                             | 类型          | 默认值                                               | 说明                                                              |
|---------------------------------|-------------|---------------------------------------------------|-----------------------------------------------------------------|
| `server.addr`                   | string      | `:8080`                                           | HTTP 和 WebSocket 监听地址。                                          |
| `server.publicBaseUrl`          | string      | 空                                                 | 后端对外基础 URL，用于拼接微软 OAuth 回调地址，也用于 CORS 允许来源。                     |
| `log.level`                     | string      | `info`                                            | 日志级别，可用 `debug`、`info`、`warn`、`error`。                          |
| `log.dir`                       | string      | `data/logs`                                       | 日志文件目录。相对路径按后端进程工作目录解析，默认即 `backend/data/logs`。                 |
| `plugin.sharedToken`            | string      | 空                                                 | 插件连接 `/internal/plugin` 的 Bearer token。                         |
| `plugin.heartbeatSeconds`       | int         | `15`                                              | 后端向插件发送 ping 的间隔秒数。                                             |
| `plugin.purchaseTimeoutSeconds` | int         | `10`                                              | 在线购票等待插件扣款/出票回执的超时秒数。                                           |
| `realtime.trainTimeoutSeconds`  | int         | `30`                                              | 列车多久未更新就视为消失，并向前端广播移除矿车图标。                                      |
| `realtime.clientSendBuffer`     | int         | `64`                                              | 每个前端实时 WebSocket 连接的发送缓冲帧数，满时丢弃最旧帧。                             |
| `auth.microsoft.clientId`       | string      | 空                                                 | Azure 应用 Client ID，为空时微软登录不可用。                                  |
| `auth.microsoft.clientSecret`   | string      | 空                                                 | Azure 应用 Client Secret。                                         |
| `auth.microsoft.redirectPath`   | string      | `/api/v1/auth/callback`                           | OAuth 回调路径，会与 `server.publicBaseUrl` 拼成完整回调 URL。                |
| `auth.jwtSecret`                | string      | 空                                                 | JWT 会话签名密钥。                                                     |
| `auth.testAuthEnabled`          | bool        | `false`                                           | 是否启用测试登录接口，仅测试环境使用。                                             |
| `auth.testAuthUUIDs`            | string list | `[]`                                              | 允许测试登录的玩家 UUID 列表。                                              |
| `db.driver`                     | string      | `mysql`                                           | 数据库类型，可选 `mysql` 或 `sqlite`。                                    |
| `db.dsn`                        | string      | `bcts:change-me@tcp(127.0.0.1:3306)/bcts_web?...` | MySQL DSN，仅 `db.driver=mysql` 时使用，建议用 `${BCTS_DB_DSN}` 从环境变量注入。 |
| `db.path`                       | string      | `data/bcts-web.db`                                | SQLite 数据库文件路径，仅 `db.driver=sqlite` 时使用。后端启动时会自动创建目录和表。         |

未配置 `auth.microsoft.clientId` 且未启用测试登录时，地图、线路和实时公开数据仍可用，登录与购票不可用。

### 前端运行时配置

`frontend:` 下的配置由后端通过 `GET /api/v1/config` 下发，前端无需重新构建即可调整样式、瓦片、世界和部分业务参数。

| 配置项                                           | 类型      | 默认值                                 | 说明                                                             |
|-----------------------------------------------|---------|-------------------------------------|----------------------------------------------------------------|
| `frontend.realtimeWsPath`                     | string  | `/api/v1/realtime`                  | 前端实时列车 WebSocket 路径。                                           |
| `frontend.defaultWorld`                       | string  | `world1`                            | 默认显示的世界名。                                                      |
| `frontend.defaultPricePerKm`                  | number  | `0.2`                               | 前端估算票价时使用的默认每公里价格。                                             |
| `frontend.currencyName`                       | string  | `帕元`                                | 票价展示使用的货币名称。                                                   |
| `frontend.themeColor`                         | string  | `#ffd400`                           | 主题色（强调色），16 进制 `#RRGGBB`；应用于按钮、高亮等 `--accent`。非法值忽略并保留默认黄色。    |
| `frontend.maxDistanceResults`                 | int     | `5`                                 | 展示「距离最近」的路线条数（`<=0` 不限制）。与插件 `search.max-distance-results` 对齐。 |
| `frontend.maxPriceResults`                    | int     | `5`                                 | 展示「票价最低」的路线条数（`<=0` 不限制）。                                      |
| `frontend.searchWeightDistance`               | number  | `0.5`                               | 混合排序距离权重：候选集内距离归一化后 ×此值，权重越小越靠前。                               |
| `frontend.searchWeightPrice`                  | number  | `0.5`                               | 混合排序票价权重：候选集内票价归一化后 ×此值。                                       |
| `frontend.minDirectResults`                   | int     | `1`                                 | 兜底：混排结果全是联程票时至少补最优的这么多条直达（`<=0` 不兜底）。                          |
| `frontend.maxTransferResults`                 | int     | `3`                                 | 最多展示的联程票（一次换乘）方案条数（`<=0` 不限制，仍受候选上限约束）。                        |
| `frontend.maxTransferCandidates`              | int     | `30`                                | 联程票寻路最多考察的候选换乘站数量，防止大线组合爆炸；优先直达路径上的经停站。                        |
| `frontend.transferMinImprovement`             | number  | `0.2`                               | 联程票最低改善比例：仅当换乘总距离 < 最短直达 ×(1−此值) 时才显示联程票。`0` 表示只要严格更短即显示。      |
| `frontend.routeSearchTimeoutMs`               | int     | `10000`                             | 路线查询超时毫秒数（前端 Web Worker 寻路）。查询期间界面显示查询中动画不卡死；超时则终止计算并提示失败。     |
| `frontend.avatarUrlTemplate`                  | string  | `https://mineskin.eu/helm/{player}` | 玩家头像 URL 模板，`{player}` 会替换为玩家名或 UUID。                          |
| `frontend.worldTiles.<world>.tileUrl`         | string  | 空                                   | 指定世界的 MapLibre raster 瓦片 URL 模板。为空时只显示纯色底图和线路。                 |
| `frontend.worldTiles.<world>.zoom`            | number  | 空                                   | 进入该世界时的初始地图缩放级别。配置后以 `center`（或数据范围中心）为镜头中心定位；不配则按数据范围自动框选缩放。  |
| `frontend.worldTiles.<world>.tileSize`        | number  | `256`                               | 单张瓦片图片像素尺寸。                                                    |
| `frontend.worldTiles.<world>.opacity`         | number  | `1`                                 | 瓦片图层透明度，范围 `0` 到 `1`；值越低线路越突出。                                 |
| `frontend.worldTiles.<world>.minNativeZoom`   | number  | `0`                                 | 瓦片源最低实际请求层级。                                                   |
| `frontend.worldTiles.<world>.maxNativeZoom`   | number  | 空                                   | 瓦片源最高实际请求层级；地图继续放大时会放大该层级瓦片，不会请求更高层级。                          |
| `frontend.worldTiles.<world>.minZoom`         | number  | `0`                                 | 该世界地图最低显示/交互缩放级别，同时也是瓦片图层最低显示级别。                               |
| `frontend.worldTiles.<world>.maxZoom`         | number  | `20`                                | 该世界地图最高显示/交互缩放级别，同时也是瓦片图层最高显示级别。                               |
| `frontend.worldTiles.<world>.scheme`          | string  | `xyz`                               | 瓦片 Y 轴编号方案，可选 `xyz` 或 `tms`。                                   |
| `frontend.worldTiles.<world>.mapScale`        | number  | `1`                                 | 1 个游戏方块对应多少「原生瓦片像素」。geojson 按游戏比例等比铺图，用它整体缩放对准瓦片。              |
| `frontend.worldTiles.<world>.mapOffset`       | `[x,z]` | `[0,0]`                             | 游戏坐标整体平移（游戏单位），用它把线路挪到与瓦片底图对齐。                                 |
| `frontend.worldTiles.<world>.center`          | `[x,z]` | 空                                   | 进入该世界时的初始镜头中心（游戏坐标 `[x,z]`）。不配则回退到数据范围中心。                      |
| `frontend.mapStyle.lineWidth`                 | number  | `3`                                 | 普通线路宽度，单位为屏幕像素。                                                |
| `frontend.mapStyle.highlightWidth`            | number  | `7`                                 | 高亮路线宽度，单位为屏幕像素。                                                |
| `frontend.mapStyle.dimOpacity`                | number  | `0.2`                               | 有高亮路线时非高亮线路透明度。                                                |
| `frontend.mapStyle.lineOpacity`               | number  | `0.9`                               | 普通线路透明度。                                                       |
| `frontend.mapStyle.stationRadius`             | number  | `6`                                 | 车站圆点半径。                                                        |
| `frontend.mapStyle.stationStrokeWidth`        | number  | `2`                                 | 车站圆点描边宽度。                                                      |
| `frontend.mapStyle.stationTextSize`           | number  | `12`                                | 车站名称字号。                                                        |
| `frontend.mapStyle.stationMergePixelDistance` | number  | `28`                                | 同名站点在屏幕距离小于该值时合并显示；寻路仍使用原始节点。                                  |
| `frontend.mapStyle.trainIconSize`             | number  | `0.6`                               | MapLibre symbol 图标缩放。                                          |
| `frontend.trainIcons.express`                 | string  | 内置 SVG data URL                     | 快速车图标，可配置为 `data:`、`http(s):` 或前端可访问的静态资源 URL。                 |
| `frontend.trainIcons.normal`                  | string  | 内置 SVG data URL                     | 普通车图标。                                                         |
| `frontend.defaultSystemLogo`                  | string  | 内置 SVG data URL                     | 铁路系统没有 logo 时使用的默认图标。                                          |

## 主要接口

- `GET /health`：存活探针。
- `GET /api/v1/config`：前端运行时配置。
- `GET /api/v1/meta`：地图版本、插件在线状态、服务器时间。
- `GET /api/v1/geojson`、`/lines`、`/systems`：地图、线路、铁路系统公开数据。
- `GET /api/v1/trains`：当前列车快照。
- `GET /api/v1/auth/login`、`/auth/callback`、`/auth/me`、`POST /auth/logout`：网页登录。
- `POST /api/v1/auth/test-login`：测试登录，仅在 `auth.testAuthEnabled` 开启时可用。
- `POST /api/v1/purchase`：在线购票（单段直达一次调用；联程票由前端对每段各调用一次）。
- `GET /api/v1/me/history?page=1&pageSize=10`：当前登录玩家的乘车历史。
- `GET /api/v1/realtime`：前端实时列车 WebSocket。
- `GET /internal/plugin`：插件内部 WebSocket，需要 `Authorization: Bearer <plugin.sharedToken>`。

## 降级行为

插件断连时，REST 继续使用数据库里的最近快照提供 `geojson`、线路和铁路系统数据；本地寻路仍可用。实时列车与购票不可用。

## Docker Compose 部署

在 `docker-compose.yml` 同级创建 `.env`：

```dotenv
BCTS_PLUGIN_TOKEN=change-me
BCTS_JWT_SECRET=replace-with-random-secret
BCTS_DB_DRIVER=mysql
BCTS_DB_PATH=data/bcts-web.db
MYSQL_DATABASE=bcts_web
MYSQL_USER=bcts
MYSQL_PASSWORD=change-me-db-password
MYSQL_ROOT_PASSWORD=change-me-root-password
MS_CLIENT_ID=
MS_CLIENT_SECRET=
```

启动：

```bash
docker compose up --build -d
```

Compose 默认会启动 `mysql:8.4`、后端和前端。MySQL 数据保存在 `mysql-data` volume 中；后端的日志和 SQLite 数据文件保存在 `backend-data` volume 中。前端访问 `http://localhost:5173`，后端暴露 `localhost:8080`，插件连接地址为 `ws://<部署机器>:8080/internal/plugin`。

## 数据库维护（生产）

后端启动时会用 `CREATE TABLE IF NOT EXISTS` 自动建表，因此“重建数据库”本质是清空 MySQL 数据卷再重启，无需单独的建表 SQL。相关脚本放在 `scripts/` 下，需在 `docker-compose.prod.yml` 同级目录（含 `.env`）执行。

### 重建数据库

破坏性操作，会删除现有全部数据，容器启动后自动重新建表。

```bash
./scripts/rebuild-db.sh
```

### 从备份恢复

把 `db-backups/*.sql.gz` 导入回正在运行的 MySQL 容器。不带参数时使用最新一份备份，也可指定文件。

```bash
./scripts/restore-db.sh                        # 恢复最新备份
./scripts/restore-db.sh db-backups/xxx.sql.gz  # 恢复指定备份
```

两个脚本都会读取 `.env` 里的 `MYSQL_DATABASE` / `MYSQL_ROOT_PASSWORD`。

## 验证命令

```bash
cd backend && go test ./...
cd frontend && npm run build
docker compose config
```
