#!/usr/bin/env bash
#
# 生产环境数据库重建脚本
# ------------------------------------------------------------------
# 作用：把 MySQL 数据卷彻底清空并重建，后端启动时会用
#       CREATE TABLE IF NOT EXISTS 自动重新建表（见 internal/store/store.go）。
#
# ⚠️  这是破坏性操作，会删除现有全部数据。脚本会先做一次备份，
#     并要求你手动输入确认后才执行销毁。
#
# 用法（在 docker-compose.prod.yml 所在目录执行）：
#   ./scripts/rebuild-db.sh
#
set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"
DB_SERVICE="mysql"
BACKEND_SERVICE="backend"
FRONTEND_SERVICE="frontend"
BACKUP_DIR="./db-backups"

# ---- 前置检查 ----------------------------------------------------
if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo "找不到 $COMPOSE_FILE，请在项目根目录（compose 文件所在处）运行。" >&2
  exit 1
fi
if [[ ! -f ".env" ]]; then
  echo "找不到 .env，脚本需要读取 MYSQL_* 变量。" >&2
  exit 1
fi

# 读取 .env 里的库名/账号（带默认值，与 compose 一致）
set -a; source .env; set +a
MYSQL_DATABASE="${MYSQL_DATABASE:-bcts_web}"
MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:?未在 .env 设置 MYSQL_ROOT_PASSWORD}"

# docker compose 命令兼容（新版是 "docker compose"）
if docker compose version >/dev/null 2>&1; then
  DC="docker compose"
else
  DC="docker-compose"
fi

echo "=================================================="
echo " 目标 compose 文件 : $COMPOSE_FILE"
echo " 目标数据库        : $MYSQL_DATABASE"
echo " 备份目录          : $BACKUP_DIR"
echo "=================================================="

# ---- 第一步：备份 ------------------------------------------------
mkdir -p "$BACKUP_DIR"
TS="$(date +%Y%m%d-%H%M%S)"
BACKUP_FILE="$BACKUP_DIR/${MYSQL_DATABASE}-${TS}.sql.gz"

echo "[1/4] 正在备份当前数据库到 $BACKUP_FILE ..."
if $DC -f "$COMPOSE_FILE" ps --status running "$DB_SERVICE" | grep -q "$DB_SERVICE"; then
  $DC -f "$COMPOSE_FILE" exec -T "$DB_SERVICE" \
    mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" \
      --single-transaction --routines --triggers "$MYSQL_DATABASE" \
    | gzip > "$BACKUP_FILE"
  echo "      备份完成：$(du -h "$BACKUP_FILE" | cut -f1)"
else
  echo "      MySQL 容器未运行，跳过备份（若数据卷仍在，重建前无法导出）。"
  read -r -p "      没有备份也继续吗？输入 yes 继续：" NOBAK
  [[ "$NOBAK" == "yes" ]] || { echo "已取消。"; exit 1; }
fi

# ---- 第二步：二次确认 --------------------------------------------
echo
echo "⚠️  下一步将删除 MySQL 数据卷并重建，所有现有数据将永久丢失。"
read -r -p "请输入数据库名 '$MYSQL_DATABASE' 以确认销毁：" CONFIRM
if [[ "$CONFIRM" != "$MYSQL_DATABASE" ]]; then
  echo "输入不匹配，已取消。数据未改动。"
  exit 1
fi

# ---- 第三步：停服并删除数据卷 ------------------------------------
echo "[2/4] 停止服务并删除 MySQL 数据卷 ..."
# 只停止依赖 DB 的服务与 DB 本身，删除命名数据卷。
$DC -f "$COMPOSE_FILE" stop "$BACKEND_SERVICE" "$DB_SERVICE" || true
$DC -f "$COMPOSE_FILE" rm -sf "$DB_SERVICE" || true

# 定位并删除 mysql 数据卷（compose 会加项目名前缀）
PROJECT="$(basename "$PWD")"
VOL_NAME="$($DC -f "$COMPOSE_FILE" config --volumes | grep -x 'mysql-data' || true)"
# 实际卷名通常为 <project>_mysql-data
docker volume rm "${PROJECT}_mysql-data" 2>/dev/null \
  || docker volume rm "mysql-data" 2>/dev/null \
  || echo "      未找到已命名的 mysql 数据卷（可能已删除），继续。"

# ---- 第四步：重启，后端自动建表 ----------------------------------
echo "[3/4] 重新拉起 MySQL 并等待健康检查 ..."
$DC -f "$COMPOSE_FILE" up -d "$DB_SERVICE"

echo "[4/4] 拉起后端（启动时自动 CREATE TABLE IF NOT EXISTS）..."
$DC -f "$COMPOSE_FILE" up -d "$BACKEND_SERVICE" "$FRONTEND_SERVICE"

echo
echo "✅ 完成。数据库已重建为空库，后端已重新建表。"
echo "   备份保存在：$BACKUP_FILE"
echo
echo "如需从备份恢复，可执行："
echo "   gunzip -c \"$BACKUP_FILE\" | $DC -f $COMPOSE_FILE exec -T $DB_SERVICE \\"
echo "     mysql -uroot -p'<你的root密码>' $MYSQL_DATABASE"
