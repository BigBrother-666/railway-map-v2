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

## 配置文件

运行配置使用 `backend/config.yml`，配置项说明见 [backend/config.example.yml](backend/config.example.yml)。

## 主要接口

- `GET /health`：存活探针。
- `GET /api/v1/config`：前端运行时配置。
- `GET /api/v1/meta`：地图版本、插件在线状态、服务器时间。
- `GET /api/v1/geojson`、`/lines`、`/systems`：地图、线路、铁路系统公开数据。
- `GET /api/v1/trains`：当前列车快照。
- `GET /api/v1/auth/login`、`/auth/callback`、`/auth/me`、`POST /auth/logout`：网页登录。
- `POST /api/v1/auth/test-login`：测试登录。
- `POST /api/v1/route/query`：路线/车票查询。
- `POST /api/v1/purchase`：在线购票（单段直达一次调用；联程票由前端对每段各调用一次）。
- `GET /api/v1/me/history?page=1&pageSize=10`：当前登录玩家的乘车历史。
- `GET /api/v1/realtime`：前端实时列车 WebSocket。
- `GET /internal/plugin`：插件内部 WebSocket，需要 Bearer token 认证。

## 降级行为

插件断连时，REST 继续使用数据库里的最近快照提供 `geojson`、线路和铁路系统数据；路线/车票查询基于该快照仍可用。实时列车与购票不可用。

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
