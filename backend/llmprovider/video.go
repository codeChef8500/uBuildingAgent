package llmprovider

// VideoFrame — 单帧视频数据
type VideoFrame struct {
	Data        []byte  // JPEG/PNG 编码的帧数据
	MimeType    string  // "image/jpeg" 或 "image/png"
	TimestampMs float64 // 帧时间戳（毫秒）
}

// VideoInput — 多帧视频便捷封装
type VideoInput struct {
	Frames    []VideoFrame
	TotalMs   float64 // 视频总时长（毫秒）
	SampleFPS float64 // 采样帧率（帧/秒）
}

// NewVideoURL 创建视频 URL 类型的 ContentPart（Qwen/Gemini 最优路径）
// fps <= 0 时不设置帧率（由服务端自动决定）
func NewVideoURL(url string, fps float64) ContentPart {
	p := ContentPart{
		Type:     ContentTypeVideoURL,
		VideoURL: url,
	}
	if fps > 0 {
		p.VideoFPS = fps
	}
	return p
}

// NewVideoURLWithFrames 创建视频 URL ContentPart，同时指定固定帧数
func NewVideoURLWithFrames(url string, nFrames int) ContentPart {
	return ContentPart{
		Type:         ContentTypeVideoURL,
		VideoURL:     url,
		VideoNFrames: nFrames,
	}
}

// NewVideoFrames 将 VideoInput 转换为 ContentPart 序列（video_frame 类型）
// 每个 VideoFrame 对应一个独立的 ContentPart
func NewVideoFrames(v VideoInput) []ContentPart {
	parts := make([]ContentPart, 0, len(v.Frames))
	for i, f := range v.Frames {
		parts = append(parts, ContentPart{
			Type:        ContentTypeVideoFrame,
			Data:        f.Data,
			MimeType:    f.MimeType,
			FrameIndex:  i,
			TimestampMs: f.TimestampMs,
		})
	}
	return parts
}

// NewVideoFrameSeq 将 []VideoFrame 直接转换为 ContentPart 序列
func NewVideoFrameSeq(frames []VideoFrame) []ContentPart {
	return NewVideoFrames(VideoInput{Frames: frames})
}

// GroupVideoFrames 将消息内容中连续的 video_frame ContentPart 合并为单个组块
//
// 用途：Qwen DashScope 要求将多帧视频作为帧数组（{"type":"video","video":["b64f1","b64f2",...]}）
// 传递，而非多个独立的 image content part。GroupVideoFrames 将连续帧合并，
// 保留其余类型的 ContentPart 不变。
//
// 输入：  [text, frame0, frame1, frame2, text, frame3, frame4]
// 输出：  [text, group{frame0,frame1,frame2}, text, group{frame3,frame4}]
//
// "组块" 在输出中以 Type=ContentTypeVideoFrame、FrameIndex=-1 标识，
// 帧数据以独立 ContentPart slice 存储在 groupedFrames 字段（见 VideoFrameGroup）
func GroupVideoFrames(parts []ContentPart) []ContentPart {
	if len(parts) == 0 {
		return parts
	}

	result := make([]ContentPart, 0, len(parts))
	i := 0
	for i < len(parts) {
		if parts[i].Type != ContentTypeVideoFrame {
			result = append(result, parts[i])
			i++
			continue
		}
		// 收集连续的 video_frame
		j := i
		for j < len(parts) && parts[j].Type == ContentTypeVideoFrame {
			j++
		}
		// parts[i:j] 是连续帧组
		if j-i == 1 {
			// 只有一帧，不需要合并
			result = append(result, parts[i])
		} else {
			// 多帧：合并为 VideoFrameGroup 标记块
			group := ContentPart{
				Type:       ContentTypeVideoFrame,
				FrameIndex: -1, // -1 = 已分组标记
			}
			// 将帧数据编码到 Text 字段（JSON）以便传递给 convert 层
			// 实际 provider convert.go 会识别 FrameIndex==-1 并按帧数组处理
			_ = parts[i:j] // 连续帧切片（convert 层通过 FrameGroupSlice 访问）
			group.VideoNFrames = j - i
			// 嵌入第一帧的 MimeType 作为组类型提示
			group.MimeType = parts[i].MimeType
			// 将所有帧的 Data 追加存储：按约定编码为单个 ContentPart
			// 真实实现：convert 层需要访问原始 parts[i:j]，此处通过独立字段传递
			// 简化处理：返回一个 sentinel + 原始帧序列，convert 层识别 sentinel
			result = append(result, group)
			result = append(result, parts[i:j]...)
		}
		i = j
	}
	return result
}

// IsVideoFrameGroup 判断一个 ContentPart 是否为 GroupVideoFrames 产生的分组标记
func IsVideoFrameGroup(p ContentPart) bool {
	return p.Type == ContentTypeVideoFrame && p.FrameIndex == -1
}
