package match

import "testing"

func TestNormalizeQuestion(t *testing.T) {
	cases := []struct {
		a, b  string
		equal bool
	}{
		{"1+1=", "1 + 1 =", true},
		{"1+1=", "１＋１＝", true},
		{"What is TCP?", "what is tcp", true},
		{"以下说法正确的是：", "以下说法正确的是", true},
		{"1+1=", "1+2=", false},
		{"苹果", "香蕉", false},
	}
	for _, c := range cases {
		got := NormalizeQuestion(c.a) == NormalizeQuestion(c.b)
		if got != c.equal {
			t.Errorf("NormalizeQuestion(%q)==NormalizeQuestion(%q) = %v, want %v", c.a, c.b, got, c.equal)
		}
	}
	if NormalizeQuestion("  ,.。 ") != "" {
		t.Errorf("punctuation-only question should normalize to empty")
	}
}
