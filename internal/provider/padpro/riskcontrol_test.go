package padpro

import (
	"strings"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestRiskControl_CheckMessageAndMediaStats(t *testing.T) {
	rc := NewRiskControl(&wechat.ProviderConfig{
		Extra: map[string]string{
			"max_messages_per_day": "2",
			"max_media_per_day":    "1",
			"message_interval_ms":  "1",
		},
	})
	rc.SetAccountCreatedAt(time.Now().AddDate(-1, 0, 0))

	if _, ok := rc.CheckMessage(); !ok {
		t.Fatal("first text message should be allowed")
	}
	if _, ok := rc.CheckMedia(); !ok {
		t.Fatal("first media message should be allowed")
	}
	if _, ok := rc.CheckMessage(); ok {
		t.Fatal("message limit should block third send")
	}
	if remaining := rc.RemainingMessages(); remaining != 0 {
		t.Fatalf("remaining messages = %d", remaining)
	}

	messages, media, groups, friends := rc.GetStats()
	if messages != 2 || media != 1 || groups != 0 || friends != 0 {
		t.Fatalf("unexpected stats: messages=%d media=%d groups=%d friends=%d", messages, media, groups, friends)
	}
	if stats := rc.StatsString(); !strings.Contains(stats, "messages=2/2") || !strings.Contains(stats, "media=1/1") {
		t.Fatalf("unexpected stats string: %s", stats)
	}
}

func TestRiskControl_SilencePeriodBlocksOperations(t *testing.T) {
	rc := NewRiskControl(&wechat.ProviderConfig{
		Extra: map[string]string{
			"new_account_silence_days": "7",
		},
	})
	rc.SetAccountCreatedAt(time.Now())

	if !rc.IsInSilencePeriod() {
		t.Fatal("expected silence period")
	}
	if _, ok := rc.CheckMessage(); ok {
		t.Fatal("message should be blocked in silence period")
	}
	if _, ok := rc.CheckMedia(); ok {
		t.Fatal("media should be blocked in silence period")
	}
	if rc.CheckGroupOperation() {
		t.Fatal("group op should be blocked in silence period")
	}
	if rc.CheckFriendOperation() {
		t.Fatal("friend op should be blocked in silence period")
	}
}

func TestRiskControl_GroupFriendCountersResetOnNewDay(t *testing.T) {
	rc := NewRiskControl(&wechat.ProviderConfig{
		Extra: map[string]string{
			"max_groups_per_day":  "1",
			"max_friends_per_day": "1",
		},
	})
	rc.SetAccountCreatedAt(time.Now().AddDate(-1, 0, 0))

	if !rc.CheckGroupOperation() {
		t.Fatal("first group op should be allowed")
	}
	if rc.CheckGroupOperation() {
		t.Fatal("second group op should be blocked")
	}
	if !rc.CheckFriendOperation() {
		t.Fatal("first friend op should be allowed")
	}
	if rc.CheckFriendOperation() {
		t.Fatal("second friend op should be blocked")
	}

	rc.mu.Lock()
	rc.counterDate = rc.counterDate.AddDate(0, 0, -1)
	rc.mu.Unlock()

	if !rc.CheckGroupOperation() {
		t.Fatal("group op should reset on new day")
	}
	if !rc.CheckFriendOperation() {
		t.Fatal("friend op should reset on new day")
	}
}
