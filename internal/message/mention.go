package message

import (
	"fmt"
	"regexp"
	"strings"
)

// Mention handling for bidirectional @mention conversion between WeChat and Matrix.

// WeChat @mention format: "@nickname " (with trailing space)
// Matrix @mention format (HTML pill): <a href="https://matrix.to/#/@user:domain">Display Name</a>

var (
	// matrixMentionRE matches Matrix HTML pills: <a href="https://matrix.to/#/@user:domain">name</a>
	matrixMentionRE = regexp.MustCompile(`<a href="https://matrix\.to/#/(@[^"]+)">([^<]+)</a>`)

	// wechatMentionRE matches WeChat @mentions: @nickname followed by space or end
	wechatMentionRE = regexp.MustCompile(`@([^\s@]+)\s?`)
)

// ConvertWeChatMentionsToMatrix converts WeChat @mentions in text to Matrix HTML pills.
// Returns (plainText, htmlText, mentionedWeChatIDs).
func ConvertWeChatMentionsToMatrix(text string, resolver func(nickname string) (matrixID, displayName string)) (string, string, []string) {
	if !strings.Contains(text, "@") {
		return text, "", nil
	}

	var mentionedIDs []string
	htmlText := escapeHTML(text)
	plainText := text

	matches := wechatMentionRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, "", nil
	}

	// Process matches in reverse to preserve indices
	for i := len(matches) - 1; i >= 0; i-- {
		fullStart, fullEnd := matches[i][0], matches[i][1]
		nameStart, nameEnd := matches[i][2], matches[i][3]

		nickname := text[nameStart:nameEnd]
		if resolver == nil {
			continue
		}

		matrixID, displayName := resolver(nickname)
		if matrixID == "" {
			continue
		}

		mentionedIDs = append(mentionedIDs, matrixID)

		// Replace in HTML
		pill := fmt.Sprintf(`<a href="https://matrix.to/#/%s">%s</a>`, matrixID, escapeHTML(displayName))
		htmlText = htmlText[:fullStart] + pill + htmlText[fullEnd:]

		// Keep plain text as-is (Matrix clients show display name)
	}

	if len(mentionedIDs) == 0 {
		return text, "", nil
	}

	return plainText, htmlText, mentionedIDs
}

// ConvertMatrixMentionsToWeChat converts Matrix HTML pills to WeChat @mentions.
// Returns the plain text with WeChat-style @mentions and list of mentioned WeChat IDs.
func ConvertMatrixMentionsToWeChat(htmlBody, plainBody string, resolver func(matrixID string) (wechatID, nickname string)) (string, []string) {
	if htmlBody == "" {
		return plainBody, nil
	}

	matches := matrixMentionRE.FindAllStringSubmatch(htmlBody, -1)
	if len(matches) == 0 {
		return plainBody, nil
	}

	result := htmlBody
	var mentionedIDs []string

	for _, match := range matches {
		matrixID := match[1]
		displayName := match[2]

		if resolver != nil {
			wechatID, nickname := resolver(matrixID)
			if wechatID != "" {
				mentionedIDs = append(mentionedIDs, wechatID)
				if nickname != "" {
					displayName = nickname
				}
			}
		}

		// Replace pill with @mention
		result = strings.Replace(result, match[0], "@"+displayName+" ", 1)
	}

	// Strip remaining HTML tags
	result = stripHTMLTags(result)

	return result, mentionedIDs
}

// escapeHTML escapes HTML special characters.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// stripHTMLTags removes HTML tags from a string.
func stripHTMLTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, "")
}
