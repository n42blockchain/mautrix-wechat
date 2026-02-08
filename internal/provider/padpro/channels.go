package padpro

import (
	"context"
	"fmt"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// ChannelsAPI provides access to WeChat Channels (视频号) via WeChatPadPro's Finder endpoints.
//
// Available endpoints:
//   - /finder/FinderSearch      - Search for Channels videos
//   - /finder/FinderFollow      - Follow a Channels author
//   - /finder/FinderUserPrepare - Prepare/prefetch user's Channels profile
//
// Channels content is primarily read-only; posting is not supported via the bridge
// due to WeChat's strict DRM and content policies.
type ChannelsAPI struct {
	client *Client
}

// NewChannelsAPI creates a new Channels API wrapper.
func NewChannelsAPI(client *Client) *ChannelsAPI {
	return &ChannelsAPI{client: client}
}

// Search searches for Channels videos by keyword.
func (c *ChannelsAPI) Search(ctx context.Context, keyword string) ([]*wechat.ChannelsVideo, error) {
	resp, err := c.client.PostJSON(ctx, "/finder/FinderSearch", &finderSearchRequest{
		Keyword: keyword,
	})
	if err != nil {
		return nil, fmt.Errorf("search channels: %w", err)
	}

	var data finderSearchResponse
	if err := c.client.ParseData(resp, &data); err != nil {
		return nil, err
	}

	videos := make([]*wechat.ChannelsVideo, 0, len(data.Videos))
	for _, v := range data.Videos {
		videos = append(videos, convertFinderVideo(v))
	}

	return videos, nil
}

// Follow follows a Channels author.
func (c *ChannelsAPI) Follow(ctx context.Context, authorID string) error {
	_, err := c.client.PostJSON(ctx, "/finder/FinderFollow", map[string]string{
		"author_id": authorID,
	})
	if err != nil {
		return fmt.Errorf("follow channels author: %w", err)
	}
	return nil
}
