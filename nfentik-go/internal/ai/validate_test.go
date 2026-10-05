package ai

import "testing"

func TestIsUnreliableAnswerRejects(t *testing.T) {
	rejects := []string{
		"",
		"   ",
		"无法确定",
		"题目信息不完整，无法作答",
		"很抱歉，我无法回答这个问题",
		"信息不足，请提供更多上下文",
		"请点击链接查看图片: https://example.com/a.png",
		"不知道",
		"N/A",
		"未知",
	}
	for _, raw := range rejects {
		if reason, bad := IsUnreliableAnswer(raw); !bad {
			t.Errorf("IsUnreliableAnswer(%q) = accept, want reject", raw)
		} else if reason == "" {
			t.Errorf("IsUnreliableAnswer(%q) rejected with empty reason", raw)
		}
	}
}

func TestIsUnreliableAnswerAccepts(t *testing.T) {
	accepts := []string{
		"A",
		"B. 网络层",
		"2",
		"1949年10月1日",
		"正确",
		"错误",
		"毛泽东",
		"TCP/IP 协议",
	}
	for _, raw := range accepts {
		if reason, bad := IsUnreliableAnswer(raw); bad {
			t.Errorf("IsUnreliableAnswer(%q) = reject (%s), want accept", raw, reason)
		}
	}
}
