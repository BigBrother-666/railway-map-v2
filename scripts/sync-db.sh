
#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# 获取脚本所在目录，不受当前工作目录影响
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="$SCRIPT_DIR/.env"

[[ -f "$ENV_FILE" ]] || {
    echo "错误：配置文件不存在：$ENV_FILE" >&2
    exit 1
}

# 加载配置
set -a
source "$ENV_FILE"
set +a

# 检查必需变量
REQUIRED_VARS=(
    SSH_TARGET SSH_PORT SSH_KEY
    REMOTE_CONTAINER REMOTE_DB_USER REMOTE_DB_PASSWORD
    DB_NAME TABLE_NAME
    LOCAL_DB_HOST LOCAL_DB_PORT LOCAL_DB_USER LOCAL_DB_PASSWORD
)

for var in "${REQUIRED_VARS[@]}"; do
    [[ -v "$var" ]] || {
        echo "错误：.env 缺少变量 $var" >&2
        exit 1
    }
done

die() {
    echo "错误：$*" >&2
    exit 1
}

command -v ssh >/dev/null || die "未安装 ssh"
command -v mysql >/dev/null || die "未安装 mysql"
command -v mysqldump >/dev/null || die "未安装 mysqldump"

[[ "$DB_NAME" =~ ^[a-zA-Z0-9_]+$ ]] ||
    die "数据库名只能包含字母、数字和下划线"

if [[ -n "$TABLE_NAME" ]]; then
    [[ "$TABLE_NAME" =~ ^[a-zA-Z0-9_]+$ ]] ||
        die "表名只能包含字母、数字和下划线"
fi

SSH_ARGS=(-p "$SSH_PORT")

if [[ -n "$SSH_KEY" ]]; then
    [[ -f "$SSH_KEY" ]] || die "SSH 密钥不存在：$SSH_KEY"
    SSH_ARGS+=(-i "$SSH_KEY")
fi

TMP_FILE="$(mktemp "$SCRIPT_DIR/.mysql-dump.XXXXXX")"
trap 'rm -f "$TMP_FILE"' EXIT

echo "==> 检查远程 SSH 连接..."
ssh "${SSH_ARGS[@]}" "$SSH_TARGET" \
    "docker inspect '$REMOTE_CONTAINER' >/dev/null" ||
    die "无法连接远程服务器或找不到 MySQL 容器"

echo "==> 从远程 MySQL 容器导出..."

REMOTE_ARGS=(
    docker exec
    -e "MYSQL_PWD=$REMOTE_DB_PASSWORD"
    "$REMOTE_CONTAINER"
    mysqldump
    -u "$REMOTE_DB_USER"
    --single-transaction
    --quick
    --default-character-set=utf8mb4
    --no-tablespaces
)

if [[ -n "$TABLE_NAME" ]]; then
    REMOTE_ARGS+=("$DB_NAME" "$TABLE_NAME")
else
    REMOTE_ARGS+=("$DB_NAME")
fi

# 对远程命令中的每个参数进行 shell 转义
REMOTE_CMD=""
for arg in "${REMOTE_ARGS[@]}"; do
    printf -v escaped '%q' "$arg"
    REMOTE_CMD+="${REMOTE_CMD:+ }$escaped"
done

ssh "${SSH_ARGS[@]}" "$SSH_TARGET" "$REMOTE_CMD" > "$TMP_FILE" ||
    die "远程导出失败，本地数据库未删除"

[[ -s "$TMP_FILE" ]] || die "导出文件为空，本地数据库未删除"

echo "==> 导出成功：$(du -h "$TMP_FILE" | cut -f1)"

export MYSQL_PWD="$LOCAL_DB_PASSWORD"

echo "==> 删除并重建本地数据库：$DB_NAME"

mysql \
    --host="$LOCAL_DB_HOST" \
    --port="$LOCAL_DB_PORT" \
    --user="$LOCAL_DB_USER" \
    --default-character-set=utf8mb4 \
    -e "DROP DATABASE IF EXISTS \`$DB_NAME\`; CREATE DATABASE \`$DB_NAME\` CHARACTER SET utf8mb4;" ||
    die "删除或重建本地数据库失败"

echo "==> 导入数据..."

mysql \
    --host="$LOCAL_DB_HOST" \
    --port="$LOCAL_DB_PORT" \
    --user="$LOCAL_DB_USER" \
    --default-character-set=utf8mb4 \
    "$DB_NAME" < "$TMP_FILE" ||
    die "导入失败，本地数据库可能不完整，请从备份恢复"

unset MYSQL_PWD

echo "==> 同步完成：$DB_NAME"
