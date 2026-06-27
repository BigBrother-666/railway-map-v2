package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv 读取一个 .env 文件，把其中 KEY=VALUE 行设进进程环境（仅当该 key 尚未设置时）。
// 纯标准库实现，无需第三方依赖。文件不存在则静默跳过——便于生产环境改用真实环境变量。
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 支持可选的 export 前缀
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// 去掉成对引号
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		// 已显式设置的环境变量优先，不覆盖
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}
