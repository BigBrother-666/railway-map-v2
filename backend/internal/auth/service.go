// Package auth 实现微软 Minecraft 账户登录与会话（见 docs/BACKEND_PROMPT.md §9）。
// 流程：微软 OAuth2 → Xbox Live → XSTS → Minecraft 服务令牌 → 拉取 MC profile（uuid/name）。
// 仅放行已通过游戏内绑定指令登记的 UUID（白名单）。会话用 JWT（HttpOnly Cookie）。
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"

	"railway-map-backend/internal/model"
)

// Whitelist 判断某 UUID 是否允许登录网页（由 store 实现）。
type Whitelist interface {
	IsBoundPlayer(uuid string) (bool, error)
}

// Service 持有 OAuth 配置、JWT 密钥与白名单。
type Service struct {
	oauth     *oauth2.Config
	jwtSecret []byte
	whitelist Whitelist
	cookie    string
}

const sessionCookie = "bcts_session"

// NewService 创建鉴权服务。redirectURL 为完整回调地址（publicBaseUrl + redirectPath）。
func NewService(clientID, clientSecret, redirectURL, jwtSecret string, wl Whitelist) *Service {
	return &Service{
		oauth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     microsoft.AzureADEndpoint("consumers"),
			Scopes:       []string{"XboxLive.signin", "offline_access"},
		},
		jwtSecret: []byte(jwtSecret),
		whitelist: wl,
		cookie:    sessionCookie,
	}
}

// NewSessionService creates a service that can issue/read session cookies without Microsoft OAuth.
func NewSessionService(jwtSecret string, wl Whitelist) *Service {
	return &Service{
		jwtSecret: []byte(jwtSecret),
		whitelist: wl,
		cookie:    sessionCookie,
	}
}

func (s *Service) OAuthConfigured() bool {
	return s != nil && s.oauth != nil
}

// AuthCodeURL 返回微软登录跳转 URL（state 防 CSRF，由调用方校验）。
func (s *Service) AuthCodeURL(state string) string {
	if s.oauth == nil {
		return ""
	}
	return s.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// Exchange 用授权码换取 MC profile，并校验白名单；通过则返回玩家身份。
func (s *Service) Exchange(ctx context.Context, code string) (*model.Player, error) {
	if s.oauth == nil {
		return nil, errors.New("oauth disabled")
	}
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}
	player, err := fetchMinecraftProfile(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	bound, err := s.whitelist.IsBoundPlayer(player.UUID)
	if err != nil {
		return nil, err
	}
	if !bound {
		return nil, ErrNotBound
	}
	return player, nil
}

// ErrNotBound 表示玩家未在游戏内绑定网页登录资格。
var ErrNotBound = errors.New("player not bound")

// IssueCookie 签发会话 JWT 并写入 HttpOnly Cookie。
func (s *Service) IssueCookie(w http.ResponseWriter, player *model.Player) error {
	claims := jwt.MapClaims{
		"uuid": player.UUID,
		"name": player.Name,
		"exp":  time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(7 * 24 * time.Hour),
	})
	return nil
}

// ClearCookie 注销会话。
func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// PlayerFromRequest 从请求 Cookie 解析并校验会话，返回玩家身份。
func (s *Service) PlayerFromRequest(r *http.Request) (*model.Player, error) {
	c, err := r.Cookie(s.cookie)
	if err != nil {
		return nil, err
	}
	token, err := jwt.Parse(c.Value, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid session")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	uuidStr, _ := claims["uuid"].(string)
	name, _ := claims["name"].(string)
	if uuidStr == "" {
		return nil, errors.New("no uuid in session")
	}
	return &model.Player{UUID: uuidStr, Name: name}, nil
}
