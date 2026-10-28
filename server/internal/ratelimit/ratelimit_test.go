package ratelimit

import (
	"testing"
	"time"
)

func TestBucketBurstAndRefill(t *testing.T) {
	b := NewBucket(3, 1.0) // 突发 3, 每秒 1
	t0 := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		if !b.Allow(t0) {
			t.Fatalf("突发内第 %d 次应放行", i+1)
		}
	}
	if b.Allow(t0) {
		t.Fatal("超过突发应拒绝")
	}
	// 1 秒后补充 1 枚。
	if !b.Allow(t0.Add(1100 * time.Millisecond)) {
		t.Fatal("1.1 秒后应放行一次")
	}
	if b.Allow(t0.Add(1150 * time.Millisecond)) {
		t.Fatal("仅补充 1 枚时第二次应拒绝")
	}
	// 3 秒后应回满(上限封顶)。
	t1 := t0.Add(5 * time.Second)
	allowed := 0
	for i := 0; i < 5; i++ {
		if b.Allow(t1) {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("回满后应连续放行 3 次, 实际 %d", allowed)
	}
}

func TestBucketRefillCap(t *testing.T) {
	b := NewBucket(2, 2.0)
	t0 := time.Unix(2000, 0)
	b.Allow(t0)
	b.Allow(t0)
	if b.Allow(t0) {
		t.Fatal("桶应已空")
	}
	// 10 秒只补到上限 2。
	t1 := t0.Add(10 * time.Second)
	if !b.Allow(t1) || !b.Allow(t1) {
		t.Fatal("补充后应有 2 枚")
	}
	if b.Allow(t1) {
		t.Fatal("封顶后不应有第 3 枚")
	}
}
