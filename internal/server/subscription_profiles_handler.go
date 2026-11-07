package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"nodectl/internal/database"
	"nodectl/internal/service"

	"github.com/google/uuid"
)

func newSubscriptionToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

func subscriptionError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	sendJSON(w, "error", message)
}

func apiSubscriptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		profiles := make([]database.SubscriptionProfile, 0)
		if err := database.DB.Order("created_at ASC, id ASC").Find(&profiles).Error; err != nil {
			subscriptionError(w, 500, "读取订阅失败")
			return
		}
		nodes, err := service.ListSubscriptionLandingNodes()
		if err != nil {
			subscriptionError(w, 500, "读取落地节点失败")
			return
		}
		sendJSON(w, "success", map[string]interface{}{"data": profiles, "landing_nodes": nodes})
	case http.MethodPost, http.MethodPut:
		// Never accept a caller-provided token, timestamps or arbitrary model fields.
		var req struct {
			ID              string   `json:"id"`
			Name            string   `json:"name"`
			Enabled         bool     `json:"enabled"`
			AllLandingNodes bool     `json:"all_landing_nodes"`
			LandingNodeKeys []string `json:"landing_node_keys"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			subscriptionError(w, 400, "订阅数据格式错误")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || utf8.RuneCountInString(req.Name) > 100 || strings.IndexFunc(req.Name, unicode.IsControl) >= 0 {
			subscriptionError(w, 400, "订阅名称需为 1–100 个字符，不能包含控制字符")
			return
		}
		var profile database.SubscriptionProfile
		if r.Method == http.MethodPut {
			if err := database.DB.Where("id = ?", req.ID).First(&profile).Error; err != nil {
				subscriptionError(w, 404, "订阅不存在")
				return
			}
		} else {
			profile.ID = uuid.NewString()
			token, err := newSubscriptionToken()
			if err != nil {
				subscriptionError(w, 500, "生成订阅链接失败")
				return
			}
			profile.Token = token
		}
		nodes, err := service.ListSubscriptionLandingNodes()
		if err != nil {
			subscriptionError(w, 500, "读取落地节点失败")
			return
		}
		valid := make(map[string]bool)
		for _, n := range nodes {
			valid[n.Key] = true
		}
		// Retain existing unavailable selections safely (e.g. a temporarily disabled
		// node), but do not accept new references to nodes outside the landing pool.
		for _, key := range profile.LandingNodeKeys {
			valid[key] = true
		}
		keys := make([]string, 0, len(req.LandingNodeKeys))
		seen := make(map[string]bool)
		for _, key := range req.LandingNodeKeys {
			if !valid[key] {
				subscriptionError(w, 400, "部分落地节点已变化，请刷新后重新选择")
				return
			}
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
		profile.Name, profile.Enabled = req.Name, req.Enabled
		profile.AllLandingNodes, profile.LandingNodeKeys = req.AllLandingNodes, keys
		if r.Method == http.MethodPost {
			err = database.DB.Create(&profile).Error
		} else {
			// Update only editable columns, preserving a concurrently rotated token.
			result := database.DB.Model(&database.SubscriptionProfile{}).Where("id = ?", profile.ID).
				Select("Name", "Enabled", "AllLandingNodes", "LandingNodeKeys").Updates(&profile)
			err = result.Error
			if err == nil && result.RowsAffected == 0 {
				subscriptionError(w, 404, "订阅不存在")
				return
			}
		}
		if err != nil {
			subscriptionError(w, 500, "保存订阅失败")
			return
		}
		if err := database.DB.First(&profile, "id = ?", profile.ID).Error; err != nil {
			subscriptionError(w, 500, "读取已保存的订阅失败")
			return
		}
		sendJSON(w, "success", map[string]interface{}{"data": profile})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			subscriptionError(w, 400, "缺少订阅 ID")
			return
		}
		result := database.DB.Where("id = ?", id).Delete(&database.SubscriptionProfile{})
		if result.Error != nil {
			subscriptionError(w, 500, "删除订阅失败")
			return
		}
		if result.RowsAffected == 0 {
			subscriptionError(w, 404, "订阅不存在")
			return
		}
		sendJSON(w, "success", "订阅已删除，原链接已失效")
	default:
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func apiResetSubscriptionToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ID == "" {
		subscriptionError(w, 400, "缺少订阅 ID")
		return
	}
	token, err := newSubscriptionToken()
	if err != nil {
		subscriptionError(w, 500, "生成订阅链接失败")
		return
	}
	result := database.DB.Model(&database.SubscriptionProfile{}).Where("id = ?", req.ID).Update("token", token)
	if result.Error != nil {
		subscriptionError(w, 500, "重置订阅链接失败")
		return
	}
	if result.RowsAffected == 0 {
		subscriptionError(w, 404, "订阅不存在")
		return
	}
	sendJSON(w, "success", map[string]interface{}{"token": token})
}
