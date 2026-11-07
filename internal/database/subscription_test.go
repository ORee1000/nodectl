package database

import (
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"nodectl/internal/logger"
)

func TestSubscriptionSchemaUpgradeAndCopy(t *testing.T) {
	oldLog := logger.Log
	logger.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger.Log = oldLog })
	openDB := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
		return db
	}
	src, dst := openDB(), openDB()
	// Upgrade a legacy database without changing its existing subscription token.
	if err := src.AutoMigrate(&SysConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := src.Create(&SysConfig{Key: "sub_token", Value: "keep-legacy-link"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, db := range []*gorm.DB{src, dst} {
		if err := autoMigrateAll(db); err != nil {
			t.Fatal(err)
		}
		if err := autoMigrateAll(db); err != nil {
			t.Fatal(err)
		} // Idempotent restarts.
	}
	var legacy SysConfig
	if err := src.First(&legacy, "key = ?", "sub_token").Error; err != nil || legacy.Value != "keep-legacy-link" {
		t.Fatal("legacy token changed")
	}
	want := SubscriptionProfile{ID: "a", Name: "分享 A", Token: "a-secret", Enabled: false, AllLandingNodes: false, LandingNodeKeys: []string{"node:a", "custom:b"}}
	if err := src.Create(&want).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTable[SubscriptionProfile](src, dst, "subscription_profiles"); err != nil {
		t.Fatal(err)
	}
	var got SubscriptionProfile
	if err := dst.First(&got, "id = ?", want.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.Token != want.Token || got.Enabled || got.AllLandingNodes || !reflect.DeepEqual(got.LandingNodeKeys, want.LandingNodeKeys) {
		t.Fatalf("subscription changed during copy: %+v", got)
	}
}
