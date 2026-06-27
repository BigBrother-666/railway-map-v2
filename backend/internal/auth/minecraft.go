package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"railway-map-backend/internal/model"
)

// fetchMinecraftProfile 用微软 OAuth access token 走完整链路换取 MC profile（uuid/name）。
// 链路：Xbox Live 用户认证 → XSTS 授权 → Minecraft 服务登录 → 拉取 profile。
func fetchMinecraftProfile(ctx context.Context, msAccessToken string) (*model.Player, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	xblToken, userHash, err := xboxLiveAuth(ctx, client, msAccessToken)
	if err != nil {
		return nil, fmt.Errorf("xbox live 认证失败: %w", err)
	}
	xstsToken, err := xstsAuth(ctx, client, xblToken)
	if err != nil {
		return nil, fmt.Errorf("xsts 授权失败: %w", err)
	}
	mcToken, err := minecraftLogin(ctx, client, userHash, xstsToken)
	if err != nil {
		return nil, fmt.Errorf("minecraft 登录失败: %w", err)
	}
	return minecraftProfile(ctx, client, mcToken)
}

func xboxLiveAuth(ctx context.Context, client *http.Client, msToken string) (token, userHash string, err error) {
	body := map[string]any{
		"Properties": map[string]any{
			"AuthMethod": "RPS",
			"SiteName":   "user.auth.xboxlive.com",
			"RpsTicket":  "d=" + msToken,
		},
		"RelyingParty": "http://auth.xboxlive.com",
		"TokenType":    "JWT",
	}
	var out struct {
		Token         string `json:"Token"`
		DisplayClaims struct {
			XUI []struct {
				UHS string `json:"uhs"`
			} `json:"xui"`
		} `json:"DisplayClaims"`
	}
	if err := postJSON(ctx, client, "https://user.auth.xboxlive.com/user/authenticate", body, &out); err != nil {
		return "", "", err
	}
	if len(out.DisplayClaims.XUI) == 0 {
		return "", "", errors.New("缺少 userHash")
	}
	return out.Token, out.DisplayClaims.XUI[0].UHS, nil
}

func xstsAuth(ctx context.Context, client *http.Client, xblToken string) (string, error) {
	body := map[string]any{
		"Properties": map[string]any{
			"SandboxId":  "RETAIL",
			"UserTokens": []string{xblToken},
		},
		"RelyingParty": "rp://api.minecraftservices.com/",
		"TokenType":    "JWT",
	}
	var out struct {
		Token string `json:"Token"`
	}
	if err := postJSON(ctx, client, "https://xsts.auth.xboxlive.com/xsts/authorize", body, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

func minecraftLogin(ctx context.Context, client *http.Client, userHash, xstsToken string) (string, error) {
	body := map[string]any{
		"identityToken": fmt.Sprintf("XBL3.0 x=%s;%s", userHash, xstsToken),
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := postJSON(ctx, client, "https://api.minecraftservices.com/authentication/login_with_xbox", body, &out); err != nil {
		return "", err
	}
	return out.AccessToken, nil
}

func minecraftProfile(ctx context.Context, client *http.Client, mcToken string) (*model.Player, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.minecraftservices.com/minecraft/profile", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+mcToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取 profile 失败: %d", resp.StatusCode)
	}
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &model.Player{UUID: dashUUID(out.ID), Name: out.Name}, nil
}

// postJSON 发送 JSON POST 并解析 JSON 响应。
func postJSON(ctx context.Context, client *http.Client, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(msg))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// dashUUID 把无连字符的 32 位 hex（MC profile id）转成带连字符的标准 UUID 形式，
// 与游戏内玩家 UUID 一致。
func dashUUID(raw string) string {
	if len(raw) != 32 || strings.Contains(raw, "-") {
		return raw
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s", raw[0:8], raw[8:12], raw[12:16], raw[16:20], raw[20:32])
}
