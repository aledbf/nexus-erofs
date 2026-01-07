package snapshotter

// LayerOrder represents the ordering convention for layer sequences.
// Different components in the container ecosystem use different orders:
//
//   - Snapshot chain (newest-first): child → parent → grandparent
//   - OCI manifest (oldest-first): base → layer1 → layer2
//   - mkfs.erofs (oldest-first): matches OCI manifest order
//   - VMDK descriptor (oldest-first): fsmeta, base, layer1, layer2
//
// This type makes the ordering explicit to prevent subtle bugs
// when passing layer lists between components.
type LayerOrder int

const (
	// OrderNewestFirst is the snapshot chain order (child points to parent).
	// Used by: snapshot metadata ParentIDs, Walk() results.
	//
	// Example: [layer3, layer2, layer1, base] where layer3 is newest.
	OrderNewestFirst LayerOrder = iota

	// OrderOldestFirst is the OCI manifest order (base layer first).
	// Used by: mkfs.erofs, VMDK descriptors, layers.manifest.
	//
	// Example: [base, layer1, layer2, layer3] where base is oldest.
	OrderOldestFirst
)

// Opposite returns the opposite ordering convention.
func (o LayerOrder) Opposite() LayerOrder {
	if o == OrderNewestFirst {
		return OrderOldestFirst
	}
	return OrderNewestFirst
}

// String implements fmt.Stringer for clear logging.
func (o LayerOrder) String() string {
	switch o {
	case OrderNewestFirst:
		return "newest-first"
	case OrderOldestFirst:
		return "oldest-first"
	default:
		return "unknown"
	}
}

// LayerSequence holds a list of layer identifiers with explicit ordering.
// This prevents accidental order assumptions when passing layers between functions.
//
// Example usage:
//
//	// From snapshot chain walk (newest-first)
//	chain := LayerSequence{IDs: parentIDs, Order: OrderNewestFirst}
//
//	// Convert to OCI order for mkfs.erofs
//	ociOrder := chain.Reverse()
//	// ociOrder.Order == OrderOldestFirst
type LayerSequence struct {
	// IDs contains the layer identifiers (snapshot IDs or paths).
	IDs []string

	// Order indicates the ordering convention of the IDs slice.
	Order LayerOrder
}

// NewNewestFirst creates a LayerSequence with newest-first ordering.
// Use this when receiving layer IDs from snapshot chain traversal.
func NewNewestFirst(ids []string) LayerSequence {
	return LayerSequence{IDs: ids, Order: OrderNewestFirst}
}

// NewOldestFirst creates a LayerSequence with oldest-first ordering.
// Use this when the layers are already in OCI manifest order.
func NewOldestFirst(ids []string) LayerSequence {
	return LayerSequence{IDs: ids, Order: OrderOldestFirst}
}

// Reverse returns a new LayerSequence with reversed order.
// The Order field is updated to reflect the new ordering.
//
// This is the canonical way to convert between snapshot chain order
// (newest-first) and OCI/VMDK order (oldest-first).
func (s LayerSequence) Reverse() LayerSequence {
	reversed := make([]string, len(s.IDs))
	for i, id := range s.IDs {
		reversed[len(s.IDs)-1-i] = id
	}
	return LayerSequence{
		IDs:   reversed,
		Order: s.Order.Opposite(),
	}
}

// Len returns the number of layers in the sequence.
func (s LayerSequence) Len() int {
	return len(s.IDs)
}

// IsEmpty returns true if the sequence has no layers.
func (s LayerSequence) IsEmpty() bool {
	return len(s.IDs) == 0
}

// ToOldestFirst returns the sequence in oldest-first order.
// If already oldest-first, returns a copy. Otherwise reverses.
func (s LayerSequence) ToOldestFirst() LayerSequence {
	if s.Order == OrderOldestFirst {
		// Return a copy to prevent mutation
		ids := make([]string, len(s.IDs))
		copy(ids, s.IDs)
		return LayerSequence{IDs: ids, Order: OrderOldestFirst}
	}
	return s.Reverse()
}

// ToNewestFirst returns the sequence in newest-first order.
// If already newest-first, returns a copy. Otherwise reverses.
func (s LayerSequence) ToNewestFirst() LayerSequence {
	if s.Order == OrderNewestFirst {
		// Return a copy to prevent mutation
		ids := make([]string, len(s.IDs))
		copy(ids, s.IDs)
		return LayerSequence{IDs: ids, Order: OrderNewestFirst}
	}
	return s.Reverse()
}
