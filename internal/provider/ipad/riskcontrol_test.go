package ipad

import (
	"testing"
	"time"
)

func TestRiskControl_DefaultConfig(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{})

	if rc.maxMessagesPerDay != 500 {
		t.Fatalf("maxMessagesPerDay: %d", rc.maxMessagesPerDay)
	}
	if rc.maxGroupsPerDay != 10 {
		t.Fatalf("maxGroupsPerDay: %d", rc.maxGroupsPerDay)
	}
	if rc.maxFriendsPerDay != 20 {
		t.Fatalf("maxFriendsPerDay: %d", rc.maxFriendsPerDay)
	}
	if rc.messageIntervalMs != 1000 {
		t.Fatalf("messageIntervalMs: %d", rc.messageIntervalMs)
	}
	if rc.newAccountSilenceDays != 3 {
		t.Fatalf("newAccountSilenceDays: %d", rc.newAccountSilenceDays)
	}
}

func TestRiskControl_CustomConfig(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MaxMessagesPerDay: 100,
		MaxGroupsPerDay:   5,
		MaxFriendsPerDay:  10,
		MessageIntervalMs: 2000,
	})

	if rc.maxMessagesPerDay != 100 {
		t.Fatalf("maxMessagesPerDay: %d", rc.maxMessagesPerDay)
	}
	if rc.maxGroupsPerDay != 5 {
		t.Fatalf("maxGroupsPerDay: %d", rc.maxGroupsPerDay)
	}
	if rc.maxFriendsPerDay != 10 {
		t.Fatalf("maxFriendsPerDay: %d", rc.maxFriendsPerDay)
	}
	if rc.messageIntervalMs != 2000 {
		t.Fatalf("messageIntervalMs: %d", rc.messageIntervalMs)
	}
}

func TestRiskControl_MessageLimit(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MaxMessagesPerDay: 3,
		MessageIntervalMs: 0,
	})

	// Should allow 3 messages
	for i := 0; i < 3; i++ {
		_, allowed := rc.CheckMessage()
		if !allowed {
			t.Fatalf("message %d should be allowed", i+1)
		}
	}

	// 4th message should be denied
	_, allowed := rc.CheckMessage()
	if allowed {
		t.Fatal("4th message should be denied")
	}

	// RemainingMessages should be 0
	if rc.RemainingMessages() != 0 {
		t.Fatalf("remaining: %d", rc.RemainingMessages())
	}
}

func TestRiskControl_GroupLimit(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MaxGroupsPerDay: 2,
	})

	if !rc.CheckGroupOperation() {
		t.Fatal("first group op should be allowed")
	}
	if !rc.CheckGroupOperation() {
		t.Fatal("second group op should be allowed")
	}
	if rc.CheckGroupOperation() {
		t.Fatal("third group op should be denied")
	}
}

func TestRiskControl_FriendLimit(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MaxFriendsPerDay: 2,
	})

	if !rc.CheckFriendOperation() {
		t.Fatal("first friend op should be allowed")
	}
	if !rc.CheckFriendOperation() {
		t.Fatal("second friend op should be allowed")
	}
	if rc.CheckFriendOperation() {
		t.Fatal("third friend op should be denied")
	}
}

func TestRiskControl_SilencePeriod(t *testing.T) {
	// Account created 1 day ago, 3 day silence
	rc := NewRiskControl(RiskControlConfig{
		NewAccountSilenceDays: 3,
		AccountCreatedAt:      time.Now().Add(-24 * time.Hour), // 1 day ago
	})

	if !rc.IsInSilencePeriod() {
		t.Fatal("should be in silence period")
	}

	_, allowed := rc.CheckMessage()
	if allowed {
		t.Fatal("message should be denied during silence")
	}

	if rc.CheckGroupOperation() {
		t.Fatal("group op should be denied during silence")
	}

	if rc.CheckFriendOperation() {
		t.Fatal("friend op should be denied during silence")
	}
}

func TestRiskControl_SilencePeriodExpired(t *testing.T) {
	// Account created 10 days ago, 3 day silence
	rc := NewRiskControl(RiskControlConfig{
		NewAccountSilenceDays: 3,
		AccountCreatedAt:      time.Now().Add(-10 * 24 * time.Hour),
		MessageIntervalMs:     0,
	})

	if rc.IsInSilencePeriod() {
		t.Fatal("silence period should have expired")
	}

	_, allowed := rc.CheckMessage()
	if !allowed {
		t.Fatal("message should be allowed after silence period")
	}
}

func TestRiskControl_GetStats(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MessageIntervalMs: 0,
	})

	rc.CheckMessage()
	rc.CheckMessage()
	rc.CheckGroupOperation()
	rc.CheckFriendOperation()
	rc.CheckFriendOperation()

	msgs, groups, friends := rc.GetStats()
	if msgs != 2 {
		t.Fatalf("messages: %d", msgs)
	}
	if groups != 1 {
		t.Fatalf("groups: %d", groups)
	}
	if friends != 2 {
		t.Fatalf("friends: %d", friends)
	}
}

func TestRiskControl_RemainingMessages(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MaxMessagesPerDay: 10,
		MessageIntervalMs: 0,
	})

	if rc.RemainingMessages() != 10 {
		t.Fatalf("remaining: %d", rc.RemainingMessages())
	}

	rc.CheckMessage()
	rc.CheckMessage()
	rc.CheckMessage()

	if rc.RemainingMessages() != 7 {
		t.Fatalf("remaining: %d", rc.RemainingMessages())
	}
}

func TestRiskControl_MessageDelay(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MessageIntervalMs: 100,
		RandomDelay:       false,
	})

	// First message: no delay expected (never sent before, lastMessageAt is zero)
	delay1, allowed := rc.CheckMessage()
	if !allowed {
		t.Fatal("first message should be allowed")
	}
	// With zero lastMessageAt, elapsed is huge, so delay should be 0
	if delay1 > 200*time.Millisecond {
		t.Fatalf("first message delay too large: %v", delay1)
	}

	// Immediately check second message: should have a delay
	delay2, allowed := rc.CheckMessage()
	if !allowed {
		t.Fatal("second message should be allowed")
	}
	// The delay should be approximately 100ms (the message interval)
	if delay2 < 50*time.Millisecond {
		t.Fatalf("expected delay around 100ms, got: %v", delay2)
	}
}

func TestRiskControl_RandomDelay(t *testing.T) {
	rc := NewRiskControl(RiskControlConfig{
		MessageIntervalMs: 100,
		RandomDelay:       true,
	})

	// Send first message
	rc.CheckMessage()

	// Second message should have a randomized delay
	delay, allowed := rc.CheckMessage()
	if !allowed {
		t.Fatal("should be allowed")
	}
	// With random delay and 100ms interval, delay should be non-negative
	if delay < 0 {
		t.Fatalf("negative delay: %v", delay)
	}
}

func TestToday(t *testing.T) {
	d := today()
	now := time.Now()

	if d.Year() != now.Year() || d.Month() != now.Month() || d.Day() != now.Day() {
		t.Fatalf("today() = %v, want date matching %v", d, now)
	}
	if d.Hour() != 0 || d.Minute() != 0 || d.Second() != 0 {
		t.Fatal("today() should be midnight")
	}
}
