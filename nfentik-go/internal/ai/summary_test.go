package ai

import (
	"strings"
	"testing"
)

func TestMajorityAnswer(t *testing.T) {
	cases := []struct {
		name    string
		answers []string
		want    string
	}{
		{"clear majority", []string{"A", "A", "B"}, "A"},
		{"case insensitive", []string{"a", "A", "b"}, "a"},
		{"whitespace trimmed", []string{" A ", "A", "B"}, " A "},
		{"single", []string{"B. 网络层"}, "B. 网络层"},
		{"empty skipped", []string{"", "C", "C"}, "C"},
		{"all empty", []string{"", "  "}, ""},
		{"no input", nil, ""},
		{"tie picks first reaching count", []string{"X", "Y"}, "X"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MajorityAnswer(c.answers); got != c.want {
				t.Errorf("MajorityAnswer(%v) = %q, want %q", c.answers, got, c.want)
			}
		})
	}
}

func TestBuildSummaryPrompt(t *testing.T) {
	prompt := BuildSummaryPrompt("TCP 是面向什么的协议？", []BaseAnswer{
		{ModelName: "GPT-4o", Answer: "A.连接"},
		{ModelName: "DeepSeek", Answer: "A"},
	})
	for _, want := range []string{"总结专家", "TCP 是面向什么的协议？", "[GPT-4o]", "A.连接", "[DeepSeek]", "最终总结答案"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("summary prompt missing %q\ngot: %s", want, prompt)
		}
	}
}
