#!/usr/bin/env bash
#
# 生产环境数据库恢复脚本
# ------------------------------------------------------------------
# 作用：把 rebuild-db.sh（或 mysqldump）生成的 .sql.gz 备份导入回
#       正在运行的 MySQL 容器，覆盖当前数据库内容。
#
# ⚠️  导入会覆盖目标库中同名表的数据。脚本会要求手动确认。
#
# 用法（在 docker-compose.prod.yml 所在目录执行）：
#   ./scripts/restore-db.sh                         # 交互选择最新备份
#   ./scripts/restore-db.sh db-backups/xxx.sql.gz   # 指定备份文件
#
set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"
DB_SERVICE="mysql"
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

set -a; source .env; set +a
MYSQL_DATABASE="${MYSQL_DATABASE:-bcts_web}"
MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:?未在 .env 设置 MYSQL_ROOT_PASSWORD}"

if docker compose version >/dev/null 2>&1; then
  DC="docker compose"
else
  DC="docker-compose"
fi

# ---- 选择备份文件 ------------------------------------------------
BACKUP_FILE="${1:-}"
if [[ -z "$BACKUP_FILE" ]]; then
  # 未指定则挑选备份目录里最新的 .sql.gz
  BACKUP_FILE="$(ls -t "$BACKUP_DIR"/*.sql.gz 2>/dev/null | head -n1 || true)"
  if [[ -z "$BACKUP_FILE" ]]; then
    echo "未指定备份文件，且 $BACKUP_DIR 下没有 .sql.gz 备份。" >&2
    echo "用法：$0 <备份文件.sql.gz>" >&2
    exit 1
  fi
  echo "未指定备份文件，将使用最新备份：$BACKUP_FILE"
fi

if [[ ! -f "$BACKUP_FILE" ]]; then
  echo "备份文件不存在：$BACKUP_FILE" >&2
  exit 1
fi

# ---- 确认 MySQL 在运行 -------------------------------------------
if ! $DC -f "$COMPOSE_FILE" ps --status running "$DB_SERVICE" | grep -q "$DB_SERVICE"; then
  echo "MySQL 容器未运行，请先启动：$DC -f $COMPOSE_FILE up -d $DB_SERVICE" >&2
  exit 1
fi

echo "=================================================="
echo " 备份文件   : $BACKUP_FILE ($(du -h "$BACKUP_FILE" | cut -f1))"
echo " 目标数据库 : $MYSQL_DATABASE"
echo "=================================================="
echo
echo "⚠️  导入会覆盖 '$MYSQL_DATABASE' 中同名表的数据。"
read -r -p "请输入数据库名 '$MYSQL_DATABASE' 以确认恢复：" CONFIRM
if [[ "$CONFIRM" != "$MYSQL_DATABASE" ]]; then
  echo "输入不匹配，已取消。数据未改动。"
  exit 1
fi

# ---- 导入 --------------------------------------------------------
echo "正在恢复 ..."
gunzip -c "$BACKUP_FILE" | \
  $DC -f "$COMPOSE_FILE" exec -T "$DB_SERVICE" \
    mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"

echo
echo "✅ 恢复完成，数据已从 $BACKUP_FILE 导入到 $MYSQL_DATABASE。"
echo "   建议重启后端以刷新连接：$DC -f $COMPOSE_FILE restart backend"
