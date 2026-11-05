package match

import "testing"

func BenchmarkQuestionHash(b *testing.B) {
	q := "性能测试题目编号 12345 计算 12345 × 2 等于多少 https://example.com/img/a.png"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		QuestionHash(q)
	}
}

func BenchmarkNormalizeQuestion(b *testing.B) {
	q := "性能测试题目编号 12345 计算 12345 × 2 等于多少"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NormalizeQuestion(q)
	}
}