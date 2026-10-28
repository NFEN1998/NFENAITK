package mogo

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInfoChoice(t *testing.T) {
	info := ParseInfo("多选题****[选项1###选项2###选项3]")
	if info.Type != "多选题" {
		t.Fatalf("Type = %q", info.Type)
	}
	want := []string{"选项1", "选项2", "选项3"}
	if !reflect.DeepEqual(info.Options, want) {
		t.Fatalf("Options = %v, want %v", info.Options, want)
	}
	options := info.QueryOptions()
	if options == nil || *options != "A. 选项1\nB. 选项2\nC. 选项3" {
		t.Fatalf("QueryOptions = %v", options)
	}
}

func TestParseInfoToleratesPipeSeparator(t *testing.T) {
	info := ParseInfo("单选题****[选项1|||选项2]")
	want := []string{"选项1", "选项2"}
	if !reflect.DeepEqual(info.Options, want) {
		t.Fatalf("Options = %v, want %v", info.Options, want)
	}
}

func TestParseInfoBlankCount(t *testing.T) {
	info := ParseInfo("填空题****3")
	if info.Type != "填空题" || info.BlankCount != 3 || len(info.Options) != 0 {
		t.Fatalf("info = %+v", info)
	}
	if prompt := info.ExtraPrompt(); !strings.Contains(prompt, "空格数量：3") {
		t.Fatalf("ExtraPrompt = %q", prompt)
	}
}

func TestParseInfoMatchingGroups(t *testing.T) {
	info := ParseInfo("连线题****[[1###2###3],[a###b###c]]")
	if info.Type != "连线题" || len(info.Groups) != 2 {
		t.Fatalf("info = %+v", info)
	}
	if !reflect.DeepEqual(info.Groups[0], []string{"1", "2", "3"}) {
		t.Fatalf("group0 = %v", info.Groups[0])
	}
	if !reflect.DeepEqual(info.Groups[1], []string{"a", "b", "c"}) {
		t.Fatalf("group1 = %v", info.Groups[1])
	}
}

func TestParseInfoClozeGroups(t *testing.T) {
	info := ParseInfo("完型填空****[[A###B],[C###D],[E###F]]")
	if len(info.Groups) != 3 {
		t.Fatalf("groups = %v", info.Groups)
	}
	if !reflect.DeepEqual(info.Groups[2], []string{"E", "F"}) {
		t.Fatalf("group2 = %v", info.Groups[2])
	}
}

func TestParseInfoDiscussion(t *testing.T) {
	info := ParseInfo("学习通讨论****1****正文:请谈谈你的看法****限制长度:50")
	if info.Type != "学习通讨论" || info.Body != "请谈谈你的看法" || info.Limit != 50 {
		t.Fatalf("info = %+v", info)
	}
}

func TestParseInfoInteraction(t *testing.T) {
	info := ParseInfo("智慧树互动****1")
	if info.Type != "智慧树互动" {
		t.Fatalf("Type = %q", info.Type)
	}
	if prompt := info.ExtraPrompt(); !strings.Contains(prompt, "****") {
		t.Fatalf("ExtraPrompt = %q", prompt)
	}
}

func TestParseInfoEmpty(t *testing.T) {
	info := ParseInfo("")
	if info.Type != "" || info.InternalType() != nil || info.QueryOptions() != nil {
		t.Fatalf("info = %+v", info)
	}
}

func TestInfoInternalType(t *testing.T) {
	cases := map[string]string{
		"名词解释": "简答题",
		"论述题":  "简答题",
		"计算题":  "简答题",
		"连线题":  "连线题",
		"排序题":  "排序题",
		"完型填空": "完型填空",
	}
	for raw, want := range cases {
		info := Info{Type: raw}
		got := info.InternalType()
		if got == nil || *got != want {
			t.Fatalf("InternalType(%q) = %v, want %q", raw, got, want)
		}
	}
}
