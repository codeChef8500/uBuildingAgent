package llmprovider

import (
	"testing"
)

func TestNewVideoURL(t *testing.T) {
	p := NewVideoURL("https://example.com/video.mp4", 2.0)
	if p.Type != ContentTypeVideoURL {
		t.Errorf("Type: got %q, want %q", p.Type, ContentTypeVideoURL)
	}
	if p.VideoURL != "https://example.com/video.mp4" {
		t.Errorf("VideoURL: got %q", p.VideoURL)
	}
	if p.VideoFPS != 2.0 {
		t.Errorf("VideoFPS: got %f, want 2.0", p.VideoFPS)
	}

	// fps <= 0 不设置
	p2 := NewVideoURL("https://example.com/v.mp4", 0)
	if p2.VideoFPS != 0 {
		t.Errorf("expected VideoFPS=0 when fps<=0")
	}
}

func TestNewVideoFrames(t *testing.T) {
	frames := []VideoFrame{
		{Data: []byte("f0"), MimeType: "image/jpeg", TimestampMs: 0},
		{Data: []byte("f1"), MimeType: "image/jpeg", TimestampMs: 500},
		{Data: []byte("f2"), MimeType: "image/jpeg", TimestampMs: 1000},
	}
	parts := NewVideoFrames(VideoInput{Frames: frames})
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	for i, p := range parts {
		if p.Type != ContentTypeVideoFrame {
			t.Errorf("parts[%d].Type: got %q", i, p.Type)
		}
		if p.FrameIndex != i {
			t.Errorf("parts[%d].FrameIndex: got %d, want %d", i, p.FrameIndex, i)
		}
	}
}

func TestGroupVideoFrames_MergesConsecutive(t *testing.T) {
	parts := []ContentPart{
		{Type: ContentTypeText, Text: "intro"},
		{Type: ContentTypeVideoFrame, FrameIndex: 0, Data: []byte("f0")},
		{Type: ContentTypeVideoFrame, FrameIndex: 1, Data: []byte("f1")},
		{Type: ContentTypeVideoFrame, FrameIndex: 2, Data: []byte("f2")},
		{Type: ContentTypeText, Text: "end"},
	}
	result := GroupVideoFrames(parts)

	// 预期：text, group-sentinel, frame0, frame1, frame2, text
	// group-sentinel 的 VideoNFrames = 3
	if len(result) < 2 {
		t.Fatalf("unexpected result length %d", len(result))
	}

	// 找到 sentinel
	sentinelIdx := -1
	for i, p := range result {
		if IsVideoFrameGroup(p) {
			sentinelIdx = i
			break
		}
	}
	if sentinelIdx == -1 {
		t.Fatal("expected a VideoFrameGroup sentinel in result")
	}
	sentinel := result[sentinelIdx]
	if sentinel.VideoNFrames != 3 {
		t.Errorf("sentinel.VideoNFrames: got %d, want 3", sentinel.VideoNFrames)
	}
}

func TestGroupVideoFrames_NonConsecutiveNotMerged(t *testing.T) {
	parts := []ContentPart{
		{Type: ContentTypeVideoFrame, FrameIndex: 0, Data: []byte("f0")},
		{Type: ContentTypeText, Text: "between"},
		{Type: ContentTypeVideoFrame, FrameIndex: 1, Data: []byte("f1")},
	}
	result := GroupVideoFrames(parts)

	sentinelCount := 0
	for _, p := range result {
		if IsVideoFrameGroup(p) {
			sentinelCount++
		}
	}
	// 两段单帧不产生 sentinel（单帧不合并）
	if sentinelCount != 0 {
		t.Errorf("expected 0 sentinels for non-consecutive single frames, got %d", sentinelCount)
	}
}

func TestGroupVideoFrames_SingleFrame(t *testing.T) {
	parts := []ContentPart{
		{Type: ContentTypeVideoFrame, FrameIndex: 0, Data: []byte("f0")},
	}
	result := GroupVideoFrames(parts)
	if len(result) != 1 {
		t.Fatalf("expected 1 part, got %d", len(result))
	}
	if IsVideoFrameGroup(result[0]) {
		t.Error("single frame should not become a group sentinel")
	}
}

func TestGroupVideoFrames_Empty(t *testing.T) {
	result := GroupVideoFrames(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
	result = GroupVideoFrames([]ContentPart{})
	if len(result) != 0 {
		t.Errorf("expected empty for empty input")
	}
}
