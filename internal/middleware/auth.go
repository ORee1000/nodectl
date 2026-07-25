package middleware

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodectl/internal/database"
	"nodectl/internal/logger"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultLoginRetryWindowSec = 600
	defaultMaxLoginRetries     = 5
	defaultLoginBlockTTLSec    = 900
)

type ipLoginAttemptState struct {
	FailedCount  int
	WindowStart  time.Time
	BlockedUntil time.Time
	LastSeen     time.Time
}

var (
	loginAttemptMu    sync.Mutex
	ipLoginAttemptMap = make(map[string]*ipLoginAttemptState)
	loginRetryWindow  = time.Duration(defaultLoginRetryWindowSec) * time.Second
	maxLoginRetries   = defaultMaxLoginRetries
	loginBlockTTL     = time.Duration(defaultLoginBlockTTLSec) * time.Second
	trustedProxyOnce  sync.Once
	trustedProxyNets  []*net.IPNet
)

func sanitizeLoginLimitConfig(retryWindowSec, maxRetries, blockTTLSec int) (int, int, int) {
	if retryWindowSec < 30 {
		retryWindowSec = 30
	}
	if retryWindowSec > 86400 {
		retryWindowSec = 86400
	}

	if maxRetries < 1 {
		maxRetries = 1
	}
	if maxRetries > 100 {
		maxRetries = 100
	}

	if blockTTLSec < 30 {
		blockTTLSec = 30
	}
	if blockTTLSec > 86400 {
		blockTTLSec = 86400
	}

	return retryWindowSec, maxRetries, blockTTLSec
}

// UpdateLoginRateLimitConfig 更新登录 IP 限流参数（秒/次数）。
func UpdateLoginRateLimitConfig(retryWindowSec, maxRetries, blockTTLSec int) {
	retryWindowSec, maxRetries, blockTTLSec = sanitizeLoginLimitConfig(retryWindowSec, maxRetries, blockTTLSec)

	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()

	loginRetryWindow = time.Duration(retryWindowSec) * time.Second
	maxLoginRetries = maxRetries
	loginBlockTTL = time.Duration(blockTTLSec) * time.Second

	// 配置变更后清空历史计数，避免旧窗口参数影响新策略。
	ipLoginAttemptMap = make(map[string]*ipLoginAttemptState)
}

// ReloadLoginRateLimitConfigFromDB 从数据库读取登录 IP 限流配置并应用到内存。
func ReloadLoginRateLimitConfigFromDB() error {
	keys := []string{
		"login_ip_retry_window_sec",
		"login_ip_max_retries",
		"login_ip_block_ttl_sec",
	}

	var cfgList []database.SysConfig
	if err := database.DB.Where("key IN ?", keys).Find(&cfgList).Error; err != nil {
		return err
	}

	raw := map[string]string{}
	for _, c := range cfgList {
		raw[c.Key] = strings.TrimSpace(c.Value)
	}

	retryWindowSec := defaultLoginRetryWindowSec
	maxRetries := defaultMaxLoginRetries
	blockTTLSec := defaultLoginBlockTTLSec

	if v, ok := raw["login_ip_retry_window_sec"]; ok && v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			retryWindowSec = parsed
		} else {
			logger.Log.Warn("登录限流配置解析失败，回退默认值", "key", "login_ip_retry_window_sec", "value", v, "error", err)
		}
	}
	if v, ok := raw["login_ip_max_retries"]; ok && v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			maxRetries = parsed
		} else {
			logger.Log.Warn("登录限流配置解析失败，回退默认值", "key", "login_ip_max_retries", "value", v, "error", err)
		}
	}
	if v, ok := raw["login_ip_block_ttl_sec"]; ok && v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			blockTTLSec = parsed
		} else {
			logger.Log.Warn("登录限流配置解析失败，回退默认值", "key", "login_ip_block_ttl_sec", "value", v, "error", err)
		}
	}

	UpdateLoginRateLimitConfig(retryWindowSec, maxRetries, blockTTLSec)
	return nil
}

// CheckLoginAttemptAllowed 检查指定 IP 当前是否允许继续尝试登录。
// 返回值: 允许登录、剩余尝试次数、若被封禁则剩余等待时长。
func CheckLoginAttemptAllowed(ip string) (bool, int, time.Duration) {
	now := time.Now()
	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()

	cleanupExpiredLoginAttemptsLocked(now)

	state, ok := ipLoginAttemptMap[ip]
	if !ok {
		return true, maxLoginRetries, 0
	}

	state.LastSeen = now

	if state.BlockedUntil.After(now) {
		return false, 0, state.BlockedUntil.Sub(now)
	}

	if now.Sub(state.WindowStart) >= loginRetryWindow {
		state.FailedCount = 0
		state.WindowStart = now
	}

	remaining := maxLoginRetries - state.FailedCount
	if remaining < 0 {
		remaining = 0
	}

	return true, remaining, 0
}

// RecordLoginFailure 记录指定 IP 的一次登录失败。
// 返回值: 是否已触发封禁、剩余可尝试次数、若封禁则封禁剩余时长。
func RecordLoginFailure(ip string) (bool, int, time.Duration) {
	now := time.Now()
	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()

	cleanupExpiredLoginAttemptsLocked(now)

	state, ok := ipLoginAttemptMap[ip]
	if !ok {
		state = &ipLoginAttemptState{
			WindowStart: now,
			LastSeen:    now,
		}
		ipLoginAttemptMap[ip] = state
	}

	state.LastSeen = now

	if state.BlockedUntil.After(now) {
		return true, 0, state.BlockedUntil.Sub(now)
	}

	if now.Sub(state.WindowStart) >= loginRetryWindow {
		state.FailedCount = 0
		state.WindowStart = now
	}

	state.FailedCount++
	remaining := maxLoginRetries - state.FailedCount
	if remaining <= 0 {
		state.BlockedUntil = now.Add(loginBlockTTL)
		state.FailedCount = maxLoginRetries
		return true, 0, loginBlockTTL
	}

	return false, remaining, 0
}

// ClearLoginFailureRecord 在登录成功后清理指定 IP 的失败计数。
func ClearLoginFailureRecord(ip string) {
	loginAttemptMu.Lock()
	defer loginAttemptMu.Unlock()
	delete(ipLoginAttemptMap, ip)
}

func cleanupExpiredLoginAttemptsLocked(now time.Time) {
	for ip, state := range ipLoginAttemptMap {
		if state == nil {
			delete(ipLoginAttemptMap, ip)
			continue
		}

		if state.BlockedUntil.After(now) {
			continue
		}

		if now.Sub(state.LastSeen) > loginRetryWindow+loginBlockTTL {
			delete(ipLoginAttemptMap, ip)
		}
	}
}

func compactUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > 160 {
		return ua[:160] + "..."
	}
	return ua
}

func loadTrustedProxyNets() {
	// 本机反向代理默认可信。Docker/外部代理需要通过
	// NODECTL_TRUSTED_PROXIES 显式配置逗号分隔的 IP/CIDR。
	raw := "127.0.0.0/8,::1/128"
	if configured := strings.TrimSpace(os.Getenv("NODECTL_TRUSTED_PROXIES")); configured != "" {
		raw += "," + configured
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil {
				if ip.To4() != nil {
					item += "/32"
				} else {
					item += "/128"
				}
			}
		}
		if _, network, err := net.ParseCIDR(item); err == nil {
			trustedProxyNets = append(trustedProxyNets, network)
		}
	}
}

func requestPeerIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	return net.ParseIP(host)
}

func isTrustedProxy(r *http.Request) bool {
	trustedProxyOnce.Do(loadTrustedProxyNets)
	ip := requestPeerIP(r)
	for _, network := range trustedProxyNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// RequestIsHTTPS reports whether TLS terminates locally or at an explicitly
// trusted reverse proxy.
func RequestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !isTrustedProxy(r) {
		return false
	}
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(proto, "https")
}

// getClientIP 从请求中提取真实客户端 IP（仅信任已配置的反向代理）
func getClientIP(r *http.Request) string {
	if isTrustedProxy(r) {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(ip) != nil {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	if ip := requestPeerIP(r); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// ClientIP exposes the same trusted-proxy-aware resolution to other packages.
func ClientIP(r *http.Request) string {
	return getClientIP(r)
}

// SecurityHeaders applies headers that are compatible with the current
// template-heavy UI. A strict CSP requires removing existing inline scripts and
// is therefore handled separately.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if RequestIsHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// Auth 鉴权中间件，用于保护需要登录才能访问的路由
func Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := getClientIP(r)
		reqPath := r.URL.Path

		// 1. 尝试从请求中获取名为 nodectl_token 的 Cookie
		cookie, err := r.Cookie("nodectl_token")
		if err != nil {
			// 没有带 Cookie，说明未登录，重定向到登录页。
			if reqPath == "/" || reqPath == "/index.html" || strings.HasPrefix(reqPath, "/api/") {
				logger.Log.Warn("未授权访问拦截",
					"reason", "请求缺少 nodectl_token Cookie",
					"ip", clientIP,
					"path", reqPath,
					"method", r.Method,
					"user_agent", compactUserAgent(r.UserAgent()),
				)
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// 2. 从数据库获取系统加密密钥 (JWT Secret)
		var secretConfig database.SysConfig
		if err := database.DB.Where("key = ?", "jwt_secret").First(&secretConfig).Error; err != nil {
			logger.Log.Error("鉴权系统异常",
				"reason", "无法从数据库读取 jwt_secret",
				"error", err,
				"ip", clientIP,
				"path", reqPath,
				"method", r.Method,
			)
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// 3. 解析并校验 JWT
		tokenString := cookie.Value
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {

			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				algoErr := fmt.Errorf("非法的签名算法: %v", token.Header["alg"])
				logger.Log.Warn("Token 校验警告",
					"reason", "尝试使用伪造的签名算法",
					"error", algoErr,
					"ip", clientIP,
				)
				return nil, algoErr
			}
			return []byte(secretConfig.Value), nil
		})

		if err != nil || !token.Valid {
			logger.Log.Warn("Token 校验失败",
				"reason", "Token无效或已过期",
				"error", err,
				"ip", clientIP,
				"path", reqPath,
				"method", r.Method,
				"user_agent", compactUserAgent(r.UserAgent()),
			)

			http.SetCookie(w, &http.Cookie{
				Name:     "nodectl_token",
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   RequestIsHTTPS(r),
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// 4. Token 校验通过，放行请求。
		next.ServeHTTP(w, r)
	}
}

func insecureHTTPExplicitlyAllowed() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NODECTL_ALLOW_INSECURE_HTTP")), "true") {
		return true
	}
	if database.DB == nil {
		return false
	}
	var cfg database.SysConfig
	return database.DB.Select("value").Where("key = ?", "sys_force_http").Take(&cfg).Error == nil &&
		strings.EqualFold(strings.TrimSpace(cfg.Value), "true")
}

// ForceHTTPS permits plain HTTP only on loopback or when the administrator has
// explicitly enabled the insecure compatibility mode.
func ForceHTTPS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peerIP := requestPeerIP(r)
		if !RequestIsHTTPS(r) && (peerIP == nil || !peerIP.IsLoopback()) && !insecureHTTPExplicitlyAllowed() {
			w.Header().Set("Connection", "close")
			http.Error(w,
				"HTTPS is required. Use a local SSH tunnel for initial setup, configure TLS, or explicitly set NODECTL_ALLOW_INSECURE_HTTP=true.",
				http.StatusUpgradeRequired)
			return
		}
		next.ServeHTTP(w, r)
	}
}
