package controllers

import (
	"testing"
)

func TestRedisStateKeyDefaultPrefix(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	key := redisStateKey("DEV001", "864201040512345")
	want := "adatrack_gps:dev001:vehicle:state:864201040512345"
	if key != want {
		t.Errorf("redisStateKey = %q, want %q", key, want)
	}
}

func TestRedisStateKeyCustomPrefix(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "adatrack_gps_")
	key := redisStateKey("ABLE01", "123456789012345")
	want := "adatrack_gps_able01:vehicle:state:123456789012345"
	if key != want {
		t.Errorf("redisStateKey = %q, want %q", key, want)
	}
}

func TestRedisStateKeyEmptyCompany(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	if got := redisStateKey("", "123456789012345"); got != "adatrack_gps:default:vehicle:state:123456789012345" {
		t.Errorf("empty company key = %q", got)
	}
}

func TestRedisStateKeyLowercasesCompany(t *testing.T) {
	t.Setenv("REDIS_KEY_PREFIX", "")
	if got := redisStateKey("ABLE01", "x"); got != "adatrack_gps:able01:vehicle:state:x" {
		t.Errorf("key = %q", got)
	}
}
