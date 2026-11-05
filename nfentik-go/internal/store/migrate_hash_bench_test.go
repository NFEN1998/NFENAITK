package store

import (
	"fmt"
	"os"
	"testing"
)

// BenchmarkWriteHashBatch measures the full Go-side batch path (hashing, SQL
// building, driver round trip) against a real database. It only runs when
// BENCH_DATABASE_URL points at a disposable database with AIResponses rows.
func BenchmarkWriteHashBatch(b *testing.B) {
	dsn := os.Getenv("BENCH_DATABASE_URL")
	if dsn == "" {
		b.Skip("BENCH_DATABASE_URL not set")
	}
	s, err := Open(dsn)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer s.Close()

	rows, err := s.query(`SELECT Id, Question FROM AIResponses ORDER BY Id LIMIT 2000`)
	if err != nil {
		b.Fatalf("select: %v", err)
	}
	items, err := scanPendingHash(rows)
	if err != nil {
		b.Fatalf("scan: %v", err)
	}
	if len(items) == 0 {
		b.Fatal("no rows")
	}
	for _, size := range []int{100, 500, 2000} {
		b.Run(fmt.Sprintf("size%d", size), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.writeQuestionHashBatch(s.db, items[:size]); err != nil {
					b.Fatalf("write: %v", err)
				}
			}
		})
	}
}