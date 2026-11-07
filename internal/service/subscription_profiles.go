package service

import (
	"crypto/sha256"
	"fmt"

	"nodectl/internal/database"
)

type subscriptionScope struct {
	all  bool
	keys map[string]bool
}

func newSubscriptionScope(profile *database.SubscriptionProfile) subscriptionScope {
	s := subscriptionScope{all: profile == nil || profile.AllLandingNodes, keys: make(map[string]bool)}
	if profile != nil {
		for _, key := range profile.LandingNodeKeys {
			s.keys[key] = true
		}
	}
	return s
}

func (s subscriptionScope) allows(routingType int, key string) bool {
	return routingType == 1 || (routingType == 2 && (s.all || s.keys[key]))
}

// Airport refreshes replace database IDs. Bind a selection to its source and
// exact link instead, so refresh/reordering cannot select a different node.
func AirportSubscriptionKey(node database.AirportNode) string {
	return fmt.Sprintf("airport:%x", sha256.Sum256([]byte(node.SubID+"\x00"+node.Link)))
}

type SubscriptionLandingNode struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Blocked bool   `json:"blocked"`
}

// ListSubscriptionLandingNodes returns selection metadata, never proxy credentials.
func ListSubscriptionLandingNodes() ([]SubscriptionLandingNode, error) {
	items := make([]SubscriptionLandingNode, 0)
	var nodes []database.NodePool
	if err := database.DB.Where("routing_type = ?", 2).Order("sort_index ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	for _, n := range nodes {
		items = append(items, SubscriptionLandingNode{"node:" + n.UUID, n.Name, "自建节点", n.IsBlocked})
	}
	var airport []database.AirportNode
	if err := database.DB.Where("routing_type = ?", 2).Order("sub_id, original_index ASC").Find(&airport).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, n := range airport {
		key := AirportSubscriptionKey(n)
		if !seen[key] {
			items = append(items, SubscriptionLandingNode{key, n.Name, "机场节点", false})
			seen[key] = true
		}
	}
	var custom []database.CustomNode
	if err := database.DB.Where("routing_type = ?", 2).Order("created_at ASC").Find(&custom).Error; err != nil {
		return nil, err
	}
	for _, n := range custom {
		items = append(items, SubscriptionLandingNode{"custom:" + n.ID, n.Name, "自定义节点", false})
	}
	return items, nil
}

// Traffic totals describe the included servers, not per-subscriber usage.
func GetSubscriptionUserinfoForProfile(profile *database.SubscriptionProfile) string {
	if profile == nil {
		return GetSubscriptionUserinfo()
	}
	var nodes []database.NodePool
	if err := database.DB.Where("routing_type IN ? AND is_blocked = ?", []int{1, 2}, false).Find(&nodes).Error; err != nil {
		return ""
	}
	scope := newSubscriptionScope(profile)
	var up, down, total int64
	for _, n := range nodes {
		if scope.allows(n.RoutingType, "node:"+n.UUID) && !shouldExcludeNodeByTrafficThreshold(&n) {
			up += n.TrafficUp
			down += n.TrafficDown
			total += n.TrafficLimit
		}
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d", up, down, total)
}
