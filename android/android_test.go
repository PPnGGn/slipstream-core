package android

import "testing"

func TestQueryMemory(t *testing.T) {
	m := QueryMemory()
	if m == nil {
		t.Fatal("QueryMemory() = nil")
	}
	if m.TotalBytes <= 0 {
		t.Errorf("TotalBytes = %d, want > 0", m.TotalBytes)
	}
	if m.RetainedBytes <= 0 || m.RetainedBytes > m.TotalBytes {
		t.Errorf("RetainedBytes = %d, want in (0, TotalBytes=%d]", m.RetainedBytes, m.TotalBytes)
	}
	if m.Goroutines < 1 {
		t.Errorf("Goroutines = %d, want >= 1", m.Goroutines)
	}
}
