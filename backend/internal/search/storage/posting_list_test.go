package storage

import "testing"

func TestPostingListAddRemoveResurrect(t *testing.T) {
	p := NewPostingList()
	p.Add(1, 1.0)
	p.Add(2, 2.0)
	p.Add(3, 3.0)
	if p.Len() != 3 {
		t.Fatalf("expected 3, got %d", p.Len())
	}
	if !p.Remove(2) {
		t.Fatal("expected Remove(2) to return true")
	}
	if p.Len() != 2 {
		t.Fatalf("expected Len=2 after remove, got %d", p.Len())
	}
	if _, ok := p.Get(2); ok {
		t.Fatal("Get(2) should report not found after remove")
	}
	// Remaining live should be 1 and 3 in DocIDs()
	ids := p.DocIDs()
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("unexpected ids: %v", ids)
	}
	// Resurrect via Add
	p.Add(2, 5.0)
	if p.Len() != 3 {
		t.Fatalf("expected Len=3 after resurrect, got %d", p.Len())
	}
	if s, ok := p.Get(2); !ok || s != 5.0 {
		t.Fatalf("Get(2) want 5.0 ok=true, got %v ok=%v", s, ok)
	}
}

func TestPostingListCompactionFiresAfterManyRemoves(t *testing.T) {
	p := NewPostingList()
	for i := uint32(0); i < 1000; i++ {
		p.Add(i, 1.0)
	}
	for i := uint32(0); i < 500; i++ {
		p.Remove(i)
	}
	if p.Len() != 500 {
		t.Fatalf("expected Len=500, got %d", p.Len())
	}
	// Verify everything we kept is still findable
	for i := uint32(500); i < 1000; i++ {
		if _, ok := p.Get(i); !ok {
			t.Fatalf("Get(%d) missing", i)
		}
	}
	// And removed ones aren't
	for i := uint32(0); i < 500; i++ {
		if _, ok := p.Get(i); ok {
			t.Fatalf("Get(%d) should be missing", i)
		}
	}
}

func TestPostingListAddDeleteAdd(t *testing.T) {
	p := NewPostingList()
	p.Add(10, 1.0)
	p.Add(20, 2.0)
	p.Remove(10)
	p.Add(15, 3.0)
	p.Add(10, 4.0)
	ids := p.DocIDs()
	if len(ids) != 3 {
		t.Fatalf("expected 3 ids, got %v", ids)
	}
	for _, want := range []uint32{10, 15, 20} {
		s, ok := p.Get(want)
		if !ok {
			t.Fatalf("Get(%d) missing", want)
		}
		_ = s
	}
}
