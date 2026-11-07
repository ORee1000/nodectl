package database

import "time"

// SubscriptionProfile is an independently revocable subscription. An empty
// selection includes no landing nodes unless AllLandingNodes is explicitly set.
type SubscriptionProfile struct {
	ID              string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Name            string    `gorm:"type:varchar(100);not null" json:"name"`
	Token           string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"token"`
	Enabled         bool      `json:"enabled"`
	AllLandingNodes bool      `json:"all_landing_nodes"`
	LandingNodeKeys []string  `gorm:"serializer:json;type:text" json:"landing_node_keys"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
