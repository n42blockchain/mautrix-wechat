package padpro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMomentsAPI_GetTimelineAndUserTimeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sns/GetSnsSync":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"objects":[{"id":"m1","user_name":"wxid_author","nick_name":"Author","content":"hello","media_list":[{"type":1,"url":"https://cdn.example.com/photo.jpg"}],"create_time":1700000000,"like_count":3,"comment_count":1,"location":{"latitude":23.1,"longitude":113.2,"poi_name":"Guangzhou"}}]}}`))
		case "/sns/SendSnsTimeLine":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode user timeline payload: %v", err)
			}
			if payload["user_name"] != "wxid_author" {
				t.Fatalf("user_name = %q", payload["user_name"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"objects":[{"id":"m2","user_name":"wxid_author","nick_name":"Author","content":"from user timeline","media_list":[],"create_time":1700000100,"like_count":5,"comment_count":2}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	api := NewMomentsAPI(client)

	timeline, err := api.GetTimeline(context.Background())
	if err != nil {
		t.Fatalf("GetTimeline error: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("timeline len = %d", len(timeline))
	}
	if timeline[0].MomentID != "m1" || timeline[0].Timestamp != 1700000000*1000 {
		t.Fatalf("unexpected timeline entry: %+v", timeline[0])
	}
	if timeline[0].Location == nil || timeline[0].Location.Poiname != "Guangzhou" {
		t.Fatalf("unexpected location: %+v", timeline[0].Location)
	}
	if len(timeline[0].MediaURLs) != 1 || timeline[0].MediaURLs[0] != "https://cdn.example.com/photo.jpg" {
		t.Fatalf("unexpected media urls: %+v", timeline[0].MediaURLs)
	}

	userTimeline, err := api.GetUserTimeline(context.Background(), "wxid_author")
	if err != nil {
		t.Fatalf("GetUserTimeline error: %v", err)
	}
	if len(userTimeline) != 1 || userTimeline[0].MomentID != "m2" {
		t.Fatalf("unexpected user timeline: %+v", userTimeline)
	}
}

func TestMomentsAPI_PostAndComment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}

		switch r.URL.Path {
		case "/sns/SendFriendCircle":
			if payload["content"] != "hello moments" {
				t.Fatalf("content = %v", payload["content"])
			}
			imageURLs, _ := payload["image_urls"].([]interface{})
			if len(imageURLs) != 2 {
				t.Fatalf("image_urls len = %d", len(imageURLs))
			}
		case "/sns/SendSnsComment":
			if payload["object_id"] != "m1" || payload["content"] != "nice" {
				t.Fatalf("unexpected comment payload: %+v", payload)
			}
		default:
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	api := NewMomentsAPI(client)

	if err := api.PostMoment(context.Background(), "hello moments", []string{"https://cdn.example.com/1.jpg", "https://cdn.example.com/2.jpg"}); err != nil {
		t.Fatalf("PostMoment error: %v", err)
	}
	if err := api.CommentOnMoment(context.Background(), "m1", "nice"); err != nil {
		t.Fatalf("CommentOnMoment error: %v", err)
	}
}

func TestChannelsAPI_SearchAndFollow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		switch r.URL.Path {
		case "/finder/FinderSearch":
			var payload finderSearchRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode search payload: %v", err)
			}
			if payload.Keyword != "n42" {
				t.Fatalf("keyword = %q", payload.Keyword)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"videos":[{"object_id":"video1","author_id":"author1","author_name":"N42","title":"Daily Update","desc":"Bridge update","cover_url":"https://cdn.example.com/cover.jpg","video_url":"https://cdn.example.com/video.mp4","duration":42,"share_url":"weixin://finder/video1","create_time":1700000200}]}}`))
		case "/finder/FinderFollow":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode follow payload: %v", err)
			}
			if payload["author_id"] != "author1" {
				t.Fatalf("author_id = %q", payload["author_id"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	api := NewChannelsAPI(client)

	videos, err := api.Search(context.Background(), "n42")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("videos len = %d", len(videos))
	}
	if videos[0].VideoID != "video1" || videos[0].Timestamp != 1700000200*1000 {
		t.Fatalf("unexpected video: %+v", videos[0])
	}
	if err := api.Follow(context.Background(), "author1"); err != nil {
		t.Fatalf("Follow error: %v", err)
	}
}
