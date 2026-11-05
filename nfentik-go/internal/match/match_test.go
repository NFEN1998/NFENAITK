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
		{"3+3=", "3+3", true},
		{"3+3=", "3×3=", false},
		{"3+3=", "3*3=", false},
		{"3+3=", "3-3=", false},
		{"1.5", "15", false},
		{"1.5+2.5", "15+25", false},
		{"1−1", "11", false},
		{"1:2", "12", false},
		{"5!", "5", false},
		{"√2", "2", false},
		{"|x|", "x", false},
		{"x²", "x", false},
		{"a=b", "ab", false},
		{"[1,2]", "12", false},
		{"(1,2)", "(12)", false},
		{"10:30", "1030", false},
		{"1,000", "1000", true},
		{"1,000+2", "1000+2", true},
		{"Hello!", "Hello", true},
		{"第1题：", "第1题", true},
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

// TestQuestionHashDistinguishesURLs guards the image-question case: the
// normalized text is identical because URLs are replaced with a placeholder,
// but different images must not share a bank row or answer each other.
func TestQuestionHashDistinguishesURLs(t *testing.T) {
	a := "请看图片 https://example.com/a.jpg"
	b := "请看图片 https://example.com/b.jpg"
	if NormalizeQuestion(a) != NormalizeQuestion(b) {
		t.Fatalf("test setup: normalized text should be equal")
	}
	if QuestionHash(a) == QuestionHash(b) {
		t.Fatalf("different image URLs produced the same QuestionHash")
	}
	if QuestionHash(a) != QuestionHash(a) {
		t.Fatalf("QuestionHash is not stable")
	}
}

func TestScoreRejectsDifferentOperators(t *testing.T) {
	// Numeric expressions with different operators must not fuzzy-match.
	for _, c := range [][2]string{
		{"3+3=", "3×3="},
		{"3+3=", "3*3="},
		{"3+3=", "3-3="},
		{"1+1", "1-1"},
	} {
		if _, ok := Score(c[0], c[1]); ok {
			t.Errorf("Score(%q, %q) accepted a different operator", c[0], c[1])
		}
	}
	// The same operator (with or without the trailing equals) still matches.
	if _, ok := Score("3+3=", "3+3"); !ok {
		t.Errorf("Score(3+3=, 3+3) rejected a compatible expression")
	}
}

func TestOperatorsCompatibleLeavesProseAlone(t *testing.T) {
	for _, c := range [][2]string{
		{"苹果(水果)", "苹果"},
		{"TCP-IP", "TCP IP"},
		{"以下说法正确的是：", "以下说法正确的是"},
	} {
		if !operatorsCompatible(c[0], c[1]) {
			t.Errorf("operatorsCompatible(%q, %q) should allow prose", c[0], c[1])
		}
	}
}

func TestScoreCanonicalizesEquivalentOperators(t *testing.T) {
	for _, c := range [][2]string{
		{"1×2", "1*2"},
		{"1÷2", "1/2"},
		{"1≤2", "1<=2"},
		{"1≥2", "1>=2"},
		{"1≠2", "1!=2"},
		{"1−1", "1-1"},
		{"x²", "x^2"},
	} {
		if _, ok := Score(c[0], c[1]); !ok {
			t.Errorf("Score(%q, %q) rejected equivalent operators", c[0], c[1])
		}
	}
}

func TestScoreRejectsFormulaSymbolLoss(t *testing.T) {
	for _, c := range [][2]string{
		{"1.5", "15"},
		{"1−1", "11"},
		{"1:2", "12"},
		{"5!", "5"},
		{"√2", "2"},
		{"|x|", "x"},
		{"x²", "x"},
		{"a=b", "ab"},
		{"1+2*3", "(1+2)*3"},
	} {
		if _, ok := Score(c[0], c[1]); ok {
			t.Errorf("Score(%q, %q) accepted a different formula", c[0], c[1])
		}
	}
}
