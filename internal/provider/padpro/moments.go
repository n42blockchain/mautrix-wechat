package padpro

import (
	"context"
	"fmt"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// MomentsAPI provides access to WeChat Moments (朋友圈) via WeChatPadPro's SNS endpoints.
//
// Available endpoints:
//   - /sns/GetSnsSync      - Sync Moments timeline
//   - /sns/SendFriendCircle - Post to Moments (high ban risk)
//   - /sns/SendSnsComment  - Comment on a Moments post
//   - /sns/UploadFriendCircleImage - Upload image for Moments post
//   - /sns/SendSnsTimeLine - Get specific user's timeline
type MomentsAPI struct {
	client *Client
}

// NewMomentsAPI creates a new Moments API wrapper.
func NewMomentsAPI(client *Client) *MomentsAPI {
	return &MomentsAPI{client: client}
}

// GetTimeline fetches the Moments timeline (friend circle feed).
func (m *MomentsAPI) GetTimeline(ctx context.Context) ([]*wechat.MomentEntry, error) {
	resp, err := m.client.PostJSON(ctx, "/sns/GetSnsSync", nil)
	if err != nil {
		return nil, fmt.Errorf("get sns timeline: %w", err)
	}

	var data snsTimelineResponse
	if err := m.client.ParseData(resp, &data); err != nil {
		return nil, err
	}

	entries := make([]*wechat.MomentEntry, 0, len(data.Objects))
	for _, obj := range data.Objects {
		entries = append(entries, convertSnsObject(obj))
	}

	return entries, nil
}

// PostMoment publishes a new Moments post.
// WARNING: This operation carries high ban risk. Use with caution.
func (m *MomentsAPI) PostMoment(ctx context.Context, content string, imageURLs []string) error {
	_, err := m.client.PostJSON(ctx, "/sns/SendFriendCircle", &snsFriendCircleRequest{
		Content:   content,
		ImageURLs: imageURLs,
	})
	if err != nil {
		return fmt.Errorf("post moment: %w", err)
	}
	return nil
}

// CommentOnMoment comments on a Moments post.
func (m *MomentsAPI) CommentOnMoment(ctx context.Context, objectID, content string) error {
	_, err := m.client.PostJSON(ctx, "/sns/SendSnsComment", &snsCommentRequest{
		ObjectID: objectID,
		Content:  content,
	})
	if err != nil {
		return fmt.Errorf("comment on moment: %w", err)
	}
	return nil
}

// GetUserTimeline fetches a specific user's Moments timeline.
func (m *MomentsAPI) GetUserTimeline(ctx context.Context, userName string) ([]*wechat.MomentEntry, error) {
	resp, err := m.client.PostJSON(ctx, "/sns/SendSnsTimeLine", map[string]string{
		"user_name": userName,
	})
	if err != nil {
		return nil, fmt.Errorf("get user timeline: %w", err)
	}

	var data snsTimelineResponse
	if err := m.client.ParseData(resp, &data); err != nil {
		return nil, err
	}

	entries := make([]*wechat.MomentEntry, 0, len(data.Objects))
	for _, obj := range data.Objects {
		entries = append(entries, convertSnsObject(obj))
	}

	return entries, nil
}
