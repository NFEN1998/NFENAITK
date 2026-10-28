package mogo

import "testing"

func TestFormatAnswerSingleChoice(t *testing.T) {
	info := Info{Type: "单选题", Options: []string{"甲", "乙"}}
	if got := FormatAnswer(info, "B"); got != "乙" {
		t.Fatalf("got %q, want 乙", got)
	}
	if got := FormatAnswer(info, "甲"); got != "甲" {
		t.Fatalf("got %q, want 甲", got)
	}
}

func TestFormatAnswerMultipleChoice(t *testing.T) {
	info := Info{Type: "多选题", Options: []string{"选项1", "选项2", "选项3"}}
	if got := FormatAnswer(info, "A###C"); got != "选项1###选项3" {
		t.Fatalf("got %q, want 选项1###选项3", got)
	}
	if got := FormatAnswer(info, "选项1|||选项3"); got != "选项1###选项3" {
		t.Fatalf("got %q, want 选项1###选项3", got)
	}
}

func TestFormatAnswerJudgement(t *testing.T) {
	info := Info{Type: "判断题"}
	cases := map[string]string{
		"正确":  "对",
		"对":   "对",
		"不错":  "对",
		"不正确": "错",
		"不对":  "错",
		"错误":  "错",
		"错":   "错",
	}
	for input, want := range cases {
		if got := FormatAnswer(info, input); got != want {
			t.Fatalf("FormatAnswer(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatAnswerCompletion(t *testing.T) {
	info := Info{Type: "填空题"}
	if got := FormatAnswer(info, "答案1###答案2"); got != "答案1###答案2" {
		t.Fatalf("got %q", got)
	}
	if got := FormatAnswer(info, "答案1|||答案2"); got != "答案1###答案2" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatAnswerMatching(t *testing.T) {
	info := Info{Type: "连线题", Groups: [][]string{{"1", "2", "3"}, {"a", "b", "c"}}}
	if got := FormatAnswer(info, "1-c###2-a###3-b"); got != "c###a###b" {
		t.Fatalf("got %q, want c###a###b", got)
	}
	if got := FormatAnswer(info, "B###C###A"); got != "b###c###a" {
		t.Fatalf("got %q, want b###c###a", got)
	}
}

func TestFormatAnswerOrdering(t *testing.T) {
	info := Info{Type: "排序题", Options: []string{"a", "b", "c", "d"}}
	if got := FormatAnswer(info, "B###C###A###D"); got != "b###c###a###d" {
		t.Fatalf("got %q, want b###c###a###d", got)
	}
}

func TestFormatAnswerCloze(t *testing.T) {
	info := Info{Type: "完型填空", Groups: [][]string{{"A", "B"}, {"C", "D"}}}
	if got := FormatAnswer(info, "B###C"); got != "B###C" {
		t.Fatalf("got %q, want B###C", got)
	}
}

func TestFormatAnswerEssayKeepsOneAnswer(t *testing.T) {
	info := Info{Type: "简答题"}
	if got := FormatAnswer(info, "第一点###第二点"); got != "第一点\n第二点" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatAnswerInteraction(t *testing.T) {
	info := Info{Type: "智慧树互动"}
	if got := FormatAnswer(info, "新的提问###回答"); got != "新的提问****回答" {
		t.Fatalf("got %q", got)
	}
	if got := FormatAnswer(info, "新的提问****回答"); got != "新的提问****回答" {
		t.Fatalf("got %q", got)
	}
}
