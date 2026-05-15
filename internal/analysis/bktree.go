package analysis

// BKTree is a metric-space tree for efficient fuzzy term lookup.
// It uses Levenshtein distance as the metric, giving O(log n) average
// search via the triangle inequality pruning property.
//
// Insert: walk tree, compute dist at each node, follow child at that dist
//         or create a new leaf if the slot is empty.
// Search: at each node, compute dist to query. If dist <= maxDist, collect.
//         Only recurse into children whose key falls in [dist-max, dist+max]
//         — all other branches are provably too far away.

type bkNode struct {
	word     string
	children map[int]*bkNode // levenshtein distance → child node
}

// BKTree is the exported tree type. Zero value is not usable — use NewBKTree.
type BKTree struct {
	root *bkNode
	size int
}

// NewBKTree creates an empty BKTree.
func NewBKTree() *BKTree {
	return &BKTree{}
}

// Add inserts word into the tree.
// Duplicate words are silently ignored (distance == 0 at some node).
func (t *BKTree) Add(word string) {
	if t.root == nil {
		t.root = &bkNode{
			word:     word,
			children: make(map[int]*bkNode),
		}
		t.size++
		return
	}

	current := t.root
	for {
		dist, ok := Levenshtein(word, current.word)
		if !ok {
			// Distance exceeds MAX_DISTANCE — treat as a large distance.
			// We still need to place the node, so fall through with a
			// synthetic large distance to find/create the right child slot.
			// Use raw DP length diff as a tiebreaker slot.
			dist = abs(len(word)-len(current.word)) + MAX_DISTANCE + 1
		}

		if dist == 0 {
			// Exact duplicate — already in tree.
			return
		}

		child, exists := current.children[dist]
		if !exists {
			current.children[dist] = &bkNode{
				word:     word,
				children: make(map[int]*bkNode),
			}
			t.size++
			return
		}
		current = child
	}
}

// Search returns all words within maxDist edits of query using BFS.
// The ok bool from Levenshtein is checked — false means early termination
// fired (distance > MAX_DISTANCE), so the node is never added to results.
func (t *BKTree) Search(query string, maxDist int) []FuzzyMatch {
	if t.root == nil {
		return nil
	}

	var results []FuzzyMatch
	stack := []*bkNode{t.root}

	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		dist, ok := Levenshtein(query, node.word)

		// Only collect if Levenshtein completed (ok==true) and within range.
		if ok && dist <= maxDist {
			results = append(results, FuzzyMatch{
				Word:     node.word,
				Distance: dist,
			})
		}

		// BKTree pruning: only recurse into children whose key is within
		// [dist-maxDist, dist+maxDist]. When ok is false we use MAX_DISTANCE+1
		// as the effective dist so the pruning window is still computed safely.
		effectiveDist := dist
		if !ok {
			effectiveDist = MAX_DISTANCE + 1
		}
		lo := effectiveDist - maxDist
		hi := effectiveDist + maxDist

		for childDist, child := range node.children {
			if childDist >= lo && childDist <= hi {
				stack = append(stack, child)
			}
		}
	}

	return results
}

// Size returns the number of words in the tree.
func (t *BKTree) Size() int {
	return t.size
}

// FuzzyMatch is a single result from BKTree.Search.
type FuzzyMatch struct {
	Word     string
	Distance int // Levenshtein distance from query
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
