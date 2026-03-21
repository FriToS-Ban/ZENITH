package compaction

import (
	"sync"
	"time"
)

type CompactorConfig struct {
	L0Threshold        int
	LevelSizeBase      int64
	LevelSizeMult      int
	MaxLevels          int
	CompactionInterval time.Duration
	Dir                string
}

type SSTableMeta struct {
	Path   string
	MinKey []byte
	MaxKey []byte
	Size   int64
	Level  int
}

// ─── Compactor ────────────────────────────────────────────────────────────────

type Compactor struct {
	mu     sync.Mutex
	levels [][]*SSTableMeta
	cfg    CompactorConfig
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

// NewCompactor creates a new Compactor with sane defaults.
func NewCompactor(cfg CompactorConfig) *Compactor

// Run starts the background compaction goroutine.
// Call this once after the engine is open.
func (c *Compactor) Run()

// Stop signals the background goroutine to exit and waits for it to finish.
// No compaction runs after Stop returns.
func (c *Compactor) Stop()

// ─── Registration ─────────────────────────────────────────────────────────────

// AddSSTable registers a newly flushed SSTable at L0.
// Called by engine after every MemTable flush.
func (c *Compactor) AddSSTable(meta *SSTableMeta)

// ─── Trigger Checks ───────────────────────────────────────────────────────────

// maybeCompact checks all trigger conditions and runs compaction if needed.
// Called by the background goroutine on every tick.
// Returns immediately if no compaction is needed.
func (c *Compactor) maybeCompact() error

// needsL0Compaction returns true when L0 file count >= L0Threshold.
func (c *Compactor) needsL0Compaction() bool

// needsLevelCompaction returns the first level that exceeds its size target,
// or -1 if all levels are within budget.
func (c *Compactor) needsLevelCompaction() int

// ─── Compaction Execution ─────────────────────────────────────────────────────

// compactL0 merges ALL L0 SSTables into L1.
// L0 is special — files can have overlapping key ranges so all of them
// must be merged together in one pass rather than one at a time.
func (c *Compactor) compactL0() error

// compactLevel picks one SSTable from level n, finds all overlapping
// SSTables in level n+1, merges them, writes new SSTables into n+1,
// and deletes the inputs.
func (c *Compactor) compactLevel(level int) error

// pickCompactionTarget picks which SSTable from level n to compact next.
// Simple strategy: pick the largest file (maximises space reclaimed per run).
func (c *Compactor) pickCompactionTarget(level int) *SSTableMeta

// findOverlapping returns all SSTables in level n+1 whose key range
// overlaps with the given min/max key range.
func (c *Compactor) findOverlapping(level int, minKey, maxKey []byte) []*SSTableMeta

// ─── Merge Core ───────────────────────────────────────────────────────────────

// mergeSSTableS is the heart of compaction.
// Takes multiple SSTable readers, merges their entries like merge sort,
// keeps only the newest version of each key, drops tombstones when safe,
// and writes the result as one or more new SSTables in targetLevel.
// Returns the metadata of the newly written SSTables.
func (c *Compactor) mergeSSTableS(inputs []*SSTableMeta, targetLevel int) ([]*SSTableMeta, error)

// ─── Manifest / Bookkeeping ───────────────────────────────────────────────────

// replaceSSTableS atomically swaps input SSTables out and new ones in
// within the Compactor's level tracking.
// Called after a successful merge — never called if merge fails.
func (c *Compactor) replaceSSTableS(inputs []*SSTableMeta, outputs []*SSTableMeta, targetLevel int)

// deleteSSTableFiles removes the actual .sst files from disk for
// SSTables that have been successfully compacted away.
func (c *Compactor) deleteSSTableFiles(metas []*SSTableMeta) error

// ─── Helpers ──────────────────────────────────────────────────────────────────

// levelSizeTarget returns the byte budget for level n.
func (c *Compactor) levelSizeTarget(level int) int64

// totalLevelSize returns total bytes used across all SSTables at level n.
func (c *Compactor) totalLevelSize(level int) int64

// newSSTablePath generates a unique file path for a new SSTable.
func (c *Compactor) newSSTablePath() string
