package main

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestAddNodeValidation checks name and virtual-node validation paths.
func TestAddNodeValidation(t *testing.T) {
	cases := []struct {
		name         string
		nodeName     string
		virtualNodes int
		wantErr      string
	}{
		{"blank name", "   ", 0, "must not be empty"},
		{"slash in name", "a/b", 0, "must not contain '/'"},
		{"negative vn", "n", -3, "must be a positive integer"},
		{"zero is allowed (defaults)", "ok", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService()
			_, err := s.AddNode(tc.nodeName, tc.virtualNodes)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDuplicateNode ensures a node cannot be added twice.
func TestDuplicateNode(t *testing.T) {
	s := NewService()
	if _, err := s.AddNode("dup", 0); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := s.AddNode("dup", 0); err == nil {
		t.Fatal("second add of same name should fail")
	}
}

// TestEmptyRingOwner confirms the empty-ring boundary.
func TestEmptyRingOwner(t *testing.T) {
	s := NewService()
	if _, err := s.Owner("k"); err == nil {
		t.Fatal("empty ring owner should error")
	}
	if _, err := s.Owners([]string{"a", "b"}); err == nil {
		t.Fatal("empty ring owners should error")
	}
}

// TestDeterministicOwner checks that the same key and node set always map to
// the same owner across repeated calls and across a re-created identical ring.
func TestDeterministicOwner(t *testing.T) {
	s1 := NewService()
	for _, n := range []string{"a", "b", "c", "d"} {
		if _, err := s1.AddNode(n, 32); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}
	seen := map[string]string{}
	for i := 0; i < 50; i++ {
		k := fmt.Sprintf("key-%d", i)
		o, err := s1.Owner(k)
		if err != nil {
			t.Fatalf("owner: %v", err)
		}
		if prev, ok := seen[k]; ok && prev != o {
			t.Fatalf("key %q owner changed: %q -> %q", k, prev, o)
		}
		seen[k] = o
	}

	// Re-create an identical ring with the same nodes and virtual-node count;
	// every key must land on the same owner.
	s2 := NewService()
	for _, n := range []string{"a", "b", "c", "d"} {
		if _, err := s2.AddNode(n, 32); err != nil {
			t.Fatalf("re-add %s: %v", n, err)
		}
	}
	for k, want := range seen {
		got, err := s2.Owner(k)
		if err != nil {
			t.Fatalf("owner s2: %v", err)
		}
		if got != want {
			t.Fatalf("key %q owner differs across identical rings: %q vs %q", k, got, want)
		}
	}
}

// TestMinimalMigration is the defining property of consistent hashing: when a
// node leaves the ring, only keys it owned are remapped; every other key keeps
// its owner.
func TestMinimalMigration(t *testing.T) {
	s := NewService()
	for _, n := range []string{"n1", "n2", "n3", "n4", "n5"} {
		if _, err := s.AddNode(n, 64); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}
	keys := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		keys = append(keys, fmt.Sprintf("object-%d", i))
	}

	before, err := s.Owners(keys)
	if err != nil {
		t.Fatalf("before: %v", err)
	}

	// Remove one node; every key NOT owned by it must keep its owner.
	if err := s.RemoveNode("n3"); err != nil {
		t.Fatalf("remove n3: %v", err)
	}
	after, err := s.Owners(keys)
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	moved := 0
	for _, k := range keys {
		b, a := before[k], after[k]
		if b == "n3" {
			// Was owned by the removed node; must have moved to one of the rest.
			if a == "n3" {
				t.Fatalf("key %q still on removed n3", k)
			}
			moved++
			continue
		}
		if a != b {
			t.Fatalf("minimal migration violated: key %q was %q, now %q (n3 was removed, not its owner)", k, b, a)
		}
	}
	if moved == 0 {
		// Statistically near-impossible with 500 keys and 5 nodes x 64 vnodes,
		// but guard against a silently degenerate ring.
		t.Fatalf("no keys were owned by n3; test cannot validate migration")
	}
}

// TestDistribution checks that virtual nodes actually balance keys across
// physical nodes (no single node gets everything) with enough vnodes.
func TestDistribution(t *testing.T) {
	s := NewService()
	for _, n := range []string{"a", "b", "c", "d"} {
		if _, err := s.AddNode(n, 100); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}
	counts := map[string]int{}
	for i := 0; i < 4000; i++ {
		o, err := s.Owner(fmt.Sprintf("k-%d", i))
		if err != nil {
			t.Fatalf("owner: %v", err)
		}
		counts[o]++
	}
	if len(counts) != 4 {
		t.Fatalf("expected keys on all 4 nodes, got %d: %v", len(counts), counts)
	}
	// With 100 vnodes per node and 4000 keys, each node should own a
	// non-trivial share. Allow generous bounds; we only assert balance is sane.
	maxC, minC := 0, math.MaxInt32
	for _, c := range counts {
		if c > maxC {
			maxC = c
		}
		if c < minC {
			minC = c
		}
	}
	if ratio := float64(maxC) / float64(minC); ratio > 4.0 {
		t.Fatalf("distribution too skewed: max=%d min=%d ratio=%.2f counts=%v", maxC, minC, ratio, counts)
	}
}

// TestRemoveAllReturnsEmpty verifies the ring becomes empty again.
func TestRemoveAllReturnsEmpty(t *testing.T) {
	s := NewService()
	if _, err := s.AddNode("solo", 0); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RemoveNode("solo"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := s.Owner("k"); err == nil {
		t.Fatal("ring should be empty after removing the only node")
	}
}

// TestRemoveMissing confirms removing an unknown node errors.
func TestRemoveMissing(t *testing.T) {
	s := NewService()
	if err := s.RemoveNode("ghost"); err == nil {
		t.Fatal("removing missing node should error")
	}
}

// TestListOrderStable checks list follows join order and survives removals.
func TestListOrderStable(t *testing.T) {
	s := NewService()
	for _, n := range []string{"first", "second", "third"} {
		if _, err := s.AddNode(n, 0); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}
	got := nodeNames(s.ListNodes())
	if want := []string{"first", "second", "third"}; !equalSlices(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	// Remove the middle node; remaining order is preserved.
	if err := s.RemoveNode("second"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got = nodeNames(s.ListNodes())
	if want := []string{"first", "third"}; !equalSlices(got, want) {
		t.Fatalf("order after remove = %v, want %v", got, want)
	}
}
