package performance

import (
	"testing"
)

func TestPerformanceBenchmark(t *testing.T) {
	// Create PerformanceBenchmark instance
	pb := NewPerformanceBenchmark()

	// Test snapshot creation benchmark
	pb.BenchmarkSnapshotCreation()

	// Test snapshot retrieval benchmark
	pb.BenchmarkSnapshotRetrieval()

	// Test chunking benchmark
	pb.BenchmarkChunking()

	// Test deduplication benchmark
	pb.BenchmarkDeduplication()

	// Test remote sync benchmark
	pb.BenchmarkRemoteSync()
}
