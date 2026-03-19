package analysis

// ─── BK-Tree ──────────────────────────────────────────────────────────────────

type bkNode struct {
	word     string
	children map[int]*bkNode // levenshtein distance → child node
}

type BKTree struct {
	root *bkNode
	size int // number of words inserted
}

func NewBKTree() *BKTree {
	return &BKTree{}
}

// Add inserts a word into the BK-Tree.
// The first word inserted becomes the root.
// Each subsequent word walks the tree using Levenshtein distance
// until it finds an empty child slot at its distance from the current node.
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
		dist, _ := Levenshtein(word, current.word)

		if dist == 0 {
			// Exact duplicate — skip, already in tree
			return
		}

		child, exists := current.children[dist]
		if !exists {
			// Empty slot at this distance — insert here
			current.children[dist] = &bkNode{
				word:     word,
				children: make(map[int]*bkNode),
			}
			t.size++
			return
		}

		// Slot taken — walk deeper
		current = child
	}
}

// Search returns all words within maxDist edits of query.
// Uses the BK-Tree metric property to prune branches:
// if a node is at distance d from the query, any result must be
// in children with distance keys in [d-maxDist, d+maxDist].
//
// Returns matched words and their distances, sorted by nothing —
// caller should sort by distance if ranking matters.

// BFS search
func (t *BKTree) Search(query string, maxDist int) []FuzzyMatch {
	if t.root == nil {
		return nil
	}

	var results []FuzzyMatch

	stack := []*bkNode{t.root}

	for len(stack) > 0 {
		// Pop
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		dist, _ := Levenshtein(query, node.word)

		if dist <= maxDist {
			results = append(results, FuzzyMatch{
				Word:     node.word,
				Distance: dist,
			})
		}

		// BK-Tree pruning: only visit children whose distance key falls
		// within [dist-maxDist, dist+maxDist]. All other branches are
		// guaranteed to be further than maxDist from the query.
		lo := dist - maxDist
		hi := dist + maxDist

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

// FuzzyMatch is a search result from BKTree.Search.
type FuzzyMatch struct {
	Word     string
	Distance int // Levenshtein distance from query
}
