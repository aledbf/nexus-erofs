package snapshotter

import (
	"fmt"
)

// LayerBlobNotFoundError indicates no EROFS layer blob exists for a snapshot.
// This typically means the EROFS differ hasn't processed the layer yet,
// or the walking differ fallback hasn't created a blob.
type LayerBlobNotFoundError struct {
	SnapshotID string
	Dir        string
	Searched   []string // Patterns searched
}

func (e *LayerBlobNotFoundError) Error() string {
	return fmt.Sprintf("layer blob not found for snapshot %s in %s (searched: %v)",
		e.SnapshotID, e.Dir, e.Searched)
}

// Is implements errors.Is for error matching.
func (e *LayerBlobNotFoundError) Is(target error) bool {
	_, ok := target.(*LayerBlobNotFoundError)
	return ok
}

// BlockMountError indicates ext4 block mount failure during commit.
// This occurs when the rwlayer.img cannot be mounted to read upper contents.
type BlockMountError struct {
	Source string
	Target string
	Cause  error
}

func (e *BlockMountError) Error() string {
	return fmt.Sprintf("mount block %s at %s: %v", e.Source, e.Target, e.Cause)
}

func (e *BlockMountError) Unwrap() error {
	return e.Cause
}

// FsmetaGenerationError indicates fsmeta creation failure.
// fsmeta generation is non-critical - callers can fall back to individual layers.
type FsmetaGenerationError struct {
	SnapshotID string
	LayerCount int
	Cause      error
}

func (e *FsmetaGenerationError) Error() string {
	return fmt.Sprintf("generate fsmeta for %s (%d layers): %v",
		e.SnapshotID, e.LayerCount, e.Cause)
}

func (e *FsmetaGenerationError) Unwrap() error {
	return e.Cause
}

// CommitConversionError indicates EROFS conversion failure during commit.
type CommitConversionError struct {
	SnapshotID string
	UpperDir   string
	Cause      error
}

func (e *CommitConversionError) Error() string {
	return fmt.Sprintf("convert snapshot %s upper %s to EROFS: %v",
		e.SnapshotID, e.UpperDir, e.Cause)
}

func (e *CommitConversionError) Unwrap() error {
	return e.Cause
}

// IncompatibleBlockSizeError indicates layers cannot be merged due to block size mismatch.
type IncompatibleBlockSizeError struct {
	LayerCount int
	Details    string
}

func (e *IncompatibleBlockSizeError) Error() string {
	return fmt.Sprintf("cannot merge %d layers: %s", e.LayerCount, e.Details)
}
