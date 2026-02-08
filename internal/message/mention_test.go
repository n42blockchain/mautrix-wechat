package message

import (
	"testing"
)

func TestConvertWeChatMentionsToMatrix_NoMentions(t *testing.T) {
	plain, html, ids := ConvertWeChatMentionsToMatrix("hello world", nil)
	if plain != "hello world" {
		t.Fatalf("plain: %q", plain)
	}
	if html != "" {
		t.Fatalf("html should be empty: %q", html)
	}
	if ids != nil {
		t.Fatalf("ids should be nil: %v", ids)
	}
}

func TestConvertWeChatMentionsToMatrix_NoAtSign(t *testing.T) {
	plain, html, ids := ConvertWeChatMentionsToMatrix("no mentions here", func(nickname string) (string, string) {
		return "@test:example.com", "Test"
	})
	if plain != "no mentions here" {
		t.Fatalf("plain: %q", plain)
	}
	if html != "" {
		t.Fatalf("html should be empty: %q", html)
	}
	if ids != nil {
		t.Fatalf("ids should be nil")
	}
}

func TestConvertWeChatMentionsToMatrix_WithResolver(t *testing.T) {
	resolver := func(nickname string) (string, string) {
		if nickname == "Alice" {
			return "@wechat_alice:example.com", "Alice (WeChat)"
		}
		return "", ""
	}

	plain, html, ids := ConvertWeChatMentionsToMatrix("hello @Alice how are you", resolver)
	if plain != "hello @Alice how are you" {
		t.Fatalf("plain: %q", plain)
	}
	if html == "" {
		t.Fatal("html should not be empty")
	}
	if len(ids) != 1 || ids[0] != "@wechat_alice:example.com" {
		t.Fatalf("ids: %v", ids)
	}

	// Verify HTML contains pill
	expected := `<a href="https://matrix.to/#/@wechat_alice:example.com">Alice (WeChat)</a>`
	if !containsStr(html, expected) {
		t.Fatalf("html should contain pill: %q", html)
	}
}

func TestConvertWeChatMentionsToMatrix_NilResolver(t *testing.T) {
	plain, html, ids := ConvertWeChatMentionsToMatrix("hello @Alice", nil)
	if plain != "hello @Alice" {
		t.Fatalf("plain: %q", plain)
	}
	if html != "" {
		t.Fatalf("html should be empty: %q", html)
	}
	if ids != nil {
		t.Fatalf("ids should be nil")
	}
}

func TestConvertWeChatMentionsToMatrix_UnresolvedMention(t *testing.T) {
	resolver := func(nickname string) (string, string) {
		return "", "" // Can't resolve
	}

	plain, html, ids := ConvertWeChatMentionsToMatrix("hello @Unknown user", resolver)
	if plain != "hello @Unknown user" {
		t.Fatalf("plain: %q", plain)
	}
	if html != "" {
		t.Fatalf("html should be empty when no mentions resolved: %q", html)
	}
	if ids != nil {
		t.Fatalf("ids should be nil")
	}
}

func TestConvertWeChatMentionsToMatrix_MultipleMentions(t *testing.T) {
	resolver := func(nickname string) (string, string) {
		switch nickname {
		case "Alice":
			return "@wechat_alice:example.com", "Alice"
		case "Bob":
			return "@wechat_bob:example.com", "Bob"
		default:
			return "", ""
		}
	}

	_, html, ids := ConvertWeChatMentionsToMatrix("@Alice and @Bob hello", resolver)
	if html == "" {
		t.Fatal("html should not be empty")
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 mentioned IDs, got %d", len(ids))
	}
}

func TestConvertMatrixMentionsToWeChat_NoHTML(t *testing.T) {
	result, ids := ConvertMatrixMentionsToWeChat("", "hello world", nil)
	if result != "hello world" {
		t.Fatalf("result: %q", result)
	}
	if ids != nil {
		t.Fatalf("ids should be nil")
	}
}

func TestConvertMatrixMentionsToWeChat_NoPills(t *testing.T) {
	result, ids := ConvertMatrixMentionsToWeChat("<b>hello</b>", "hello", nil)
	if result != "hello" {
		t.Fatalf("result: %q", result)
	}
	if ids != nil {
		t.Fatalf("ids should be nil")
	}
}

func TestConvertMatrixMentionsToWeChat_WithPill(t *testing.T) {
	htmlBody := `Hello <a href="https://matrix.to/#/@wechat_alice:example.com">Alice</a> how are you`
	resolver := func(matrixID string) (string, string) {
		if matrixID == "@wechat_alice:example.com" {
			return "wxid_alice", "小明"
		}
		return "", ""
	}

	result, ids := ConvertMatrixMentionsToWeChat(htmlBody, "Hello Alice how are you", resolver)
	if len(ids) != 1 || ids[0] != "wxid_alice" {
		t.Fatalf("ids: %v", ids)
	}
	if !containsStr(result, "@小明") {
		t.Fatalf("result should contain @小明: %q", result)
	}
}

func TestConvertMatrixMentionsToWeChat_NilResolver(t *testing.T) {
	htmlBody := `Hello <a href="https://matrix.to/#/@user:example.com">User</a>`
	result, ids := ConvertMatrixMentionsToWeChat(htmlBody, "Hello User", nil)
	if !containsStr(result, "@User") {
		t.Fatalf("should use display name from pill: %q", result)
	}
	if ids != nil {
		t.Fatalf("ids should be nil when no resolver")
	}
}

func TestConvertMatrixMentionsToWeChat_MultiplePills(t *testing.T) {
	htmlBody := `<a href="https://matrix.to/#/@a:ex.com">A</a> and <a href="https://matrix.to/#/@b:ex.com">B</a>`
	resolver := func(matrixID string) (string, string) {
		switch matrixID {
		case "@a:ex.com":
			return "wxid_a", "用户A"
		case "@b:ex.com":
			return "wxid_b", "用户B"
		default:
			return "", ""
		}
	}

	result, ids := ConvertMatrixMentionsToWeChat(htmlBody, "A and B", resolver)
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	if !containsStr(result, "@用户A") || !containsStr(result, "@用户B") {
		t.Fatalf("result: %q", result)
	}
}

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"<script>", "&lt;script&gt;"},
		{"a & b", "a &amp; b"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"<a>&b\"c", "&lt;a&gt;&amp;b&quot;c"},
	}

	for _, tt := range tests {
		result := escapeHTML(tt.input)
		if result != tt.expected {
			t.Errorf("escapeHTML(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"<b>bold</b>", "bold"},
		{"<a href=\"url\">link</a> text", "link text"},
		{"no tags", "no tags"},
		{"<p>para</p><br/>next", "paranext"},
	}

	for _, tt := range tests {
		result := stripHTMLTags(tt.input)
		if result != tt.expected {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
