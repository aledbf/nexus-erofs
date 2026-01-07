package snapshotter

import (
	"testing"
)

func TestLayerOrderString(t *testing.T) {
	tests := []struct {
		order    LayerOrder
		expected string
	}{
		{OrderNewestFirst, "newest-first"},
		{OrderOldestFirst, "oldest-first"},
		{LayerOrder(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.order.String()
		if got != tt.expected {
			t.Errorf("LayerOrder(%d).String() = %q, want %q", tt.order, got, tt.expected)
		}
	}
}

func TestLayerOrderOpposite(t *testing.T) {
	if OrderNewestFirst.Opposite() != OrderOldestFirst {
		t.Error("NewestFirst.Opposite() should be OldestFirst")
	}
	if OrderOldestFirst.Opposite() != OrderNewestFirst {
		t.Error("OldestFirst.Opposite() should be NewestFirst")
	}
}

func TestLayerSequenceReverse(t *testing.T) {
	original := LayerSequence{
		IDs:   []string{"layer3", "layer2", "layer1", "base"},
		Order: OrderNewestFirst,
	}

	reversed := original.Reverse()

	// Check order changed
	if reversed.Order != OrderOldestFirst {
		t.Errorf("reversed.Order = %v, want %v", reversed.Order, OrderOldestFirst)
	}

	// Check IDs reversed
	expected := []string{"base", "layer1", "layer2", "layer3"}
	if len(reversed.IDs) != len(expected) {
		t.Fatalf("reversed.IDs length = %d, want %d", len(reversed.IDs), len(expected))
	}
	for i, id := range reversed.IDs {
		if id != expected[i] {
			t.Errorf("reversed.IDs[%d] = %q, want %q", i, id, expected[i])
		}
	}

	// Original should be unchanged
	if original.Order != OrderNewestFirst {
		t.Error("original.Order should be unchanged")
	}
	if original.IDs[0] != "layer3" {
		t.Error("original.IDs should be unchanged")
	}
}

func TestLayerSequenceDoubleReverse(t *testing.T) {
	original := LayerSequence{
		IDs:   []string{"a", "b", "c"},
		Order: OrderNewestFirst,
	}

	doubleReversed := original.Reverse().Reverse()

	if doubleReversed.Order != original.Order {
		t.Errorf("double reverse order = %v, want %v", doubleReversed.Order, original.Order)
	}

	for i, id := range doubleReversed.IDs {
		if id != original.IDs[i] {
			t.Errorf("double reverse IDs[%d] = %q, want %q", i, id, original.IDs[i])
		}
	}
}

func TestLayerSequenceToOldestFirst(t *testing.T) {
	t.Run("from newest-first", func(t *testing.T) {
		seq := NewNewestFirst([]string{"c", "b", "a"})
		result := seq.ToOldestFirst()

		if result.Order != OrderOldestFirst {
			t.Errorf("Order = %v, want OldestFirst", result.Order)
		}
		if result.IDs[0] != "a" {
			t.Errorf("first ID = %q, want 'a' (oldest)", result.IDs[0])
		}
	})

	t.Run("from oldest-first", func(t *testing.T) {
		seq := NewOldestFirst([]string{"a", "b", "c"})
		result := seq.ToOldestFirst()

		if result.Order != OrderOldestFirst {
			t.Errorf("Order = %v, want OldestFirst", result.Order)
		}
		// Should be a copy, not reverse
		if result.IDs[0] != "a" {
			t.Errorf("first ID = %q, want 'a'", result.IDs[0])
		}
	})
}

func TestLayerSequenceToNewestFirst(t *testing.T) {
	t.Run("from oldest-first", func(t *testing.T) {
		seq := NewOldestFirst([]string{"a", "b", "c"})
		result := seq.ToNewestFirst()

		if result.Order != OrderNewestFirst {
			t.Errorf("Order = %v, want NewestFirst", result.Order)
		}
		if result.IDs[0] != "c" {
			t.Errorf("first ID = %q, want 'c' (newest)", result.IDs[0])
		}
	})

	t.Run("from newest-first", func(t *testing.T) {
		seq := NewNewestFirst([]string{"c", "b", "a"})
		result := seq.ToNewestFirst()

		if result.Order != OrderNewestFirst {
			t.Errorf("Order = %v, want NewestFirst", result.Order)
		}
		// Should be a copy, not reverse
		if result.IDs[0] != "c" {
			t.Errorf("first ID = %q, want 'c'", result.IDs[0])
		}
	})
}

func TestLayerSequenceLen(t *testing.T) {
	empty := LayerSequence{}
	if empty.Len() != 0 {
		t.Errorf("empty.Len() = %d, want 0", empty.Len())
	}

	three := NewNewestFirst([]string{"a", "b", "c"})
	if three.Len() != 3 {
		t.Errorf("three.Len() = %d, want 3", three.Len())
	}
}

func TestLayerSequenceIsEmpty(t *testing.T) {
	empty := LayerSequence{}
	if !empty.IsEmpty() {
		t.Error("empty sequence should be empty")
	}

	nonEmpty := NewNewestFirst([]string{"a"})
	if nonEmpty.IsEmpty() {
		t.Error("non-empty sequence should not be empty")
	}
}

func TestNewNewestFirst(t *testing.T) {
	ids := []string{"c", "b", "a"}
	seq := NewNewestFirst(ids)

	if seq.Order != OrderNewestFirst {
		t.Errorf("Order = %v, want NewestFirst", seq.Order)
	}
	if len(seq.IDs) != 3 {
		t.Errorf("IDs length = %d, want 3", len(seq.IDs))
	}
}

func TestNewOldestFirst(t *testing.T) {
	ids := []string{"a", "b", "c"}
	seq := NewOldestFirst(ids)

	if seq.Order != OrderOldestFirst {
		t.Errorf("Order = %v, want OldestFirst", seq.Order)
	}
	if len(seq.IDs) != 3 {
		t.Errorf("IDs length = %d, want 3", len(seq.IDs))
	}
}

func TestLayerSequenceCopyOnConvert(t *testing.T) {
	// Ensure ToOldestFirst/ToNewestFirst return copies, not references
	original := NewNewestFirst([]string{"a", "b", "c"})
	result := original.ToNewestFirst()

	// Modify result
	result.IDs[0] = "modified"

	// Original should be unchanged
	if original.IDs[0] != "a" {
		t.Error("original was modified when result was changed")
	}
}
