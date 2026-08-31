package board

import "sort"

// orderWithDependencies groups MyPRRow entries into dependency chains —
// stacked PRs where one PR's BaseRef is another's HeadRef, within the same
// repo — and returns them flattened with Depth set (0 for a chain's root,
// increasing by 1 per level of stacking). Chain roots (and standalone PRs,
// which are trivially chains of one) are ordered by status priority then
// age (oldest first), matching the plain non-chain ordering used before
// dependency detection existed. Each parent's direct children use the same
// comparator among themselves.
func orderWithDependencies(rows []MyPRRow) []MyPRRow {
	n := len(rows)
	if n == 0 {
		return rows
	}

	// Index PRs by (repo, head branch) to find "who does this PR stack on".
	byHead := make(map[string]int, n)
	for i, r := range rows {
		if r.HeadRef != "" {
			byHead[r.Repo+"\x00"+r.HeadRef] = i
		}
	}

	children := make(map[int][]int)
	isChild := make(map[int]bool)
	for i, r := range rows {
		if r.BaseRef == "" {
			continue
		}
		if pi, ok := byHead[r.Repo+"\x00"+r.BaseRef]; ok && pi != i {
			children[pi] = append(children[pi], i)
			isChild[i] = true
		}
	}

	less := func(idxs []int) func(a, b int) bool {
		return func(a, b int) bool {
			ra, rb := rows[idxs[a]], rows[idxs[b]]
			if ra.Status.Priority() != rb.Status.Priority() {
				return ra.Status.Priority() < rb.Status.Priority()
			}
			return ra.ageSecs > rb.ageSecs
		}
	}
	for parent, idxs := range children {
		sort.Slice(idxs, less(idxs))
		children[parent] = idxs
	}

	var roots []int
	for i := range rows {
		if !isChild[i] {
			roots = append(roots, i)
		}
	}
	sort.Slice(roots, less(roots))

	visited := make(map[int]bool, n)
	var dfs func(i, depth int, out *[]MyPRRow)
	dfs = func(i, depth int, out *[]MyPRRow) {
		if visited[i] {
			return // defends against a malformed cycle; shouldn't occur in practice
		}
		visited[i] = true
		row := rows[i]
		row.Depth = depth
		*out = append(*out, row)
		for _, ci := range children[i] {
			dfs(ci, depth+1, out)
		}
	}

	out := make([]MyPRRow, 0, n)
	for _, ri := range roots {
		dfs(ri, 0, &out)
	}
	// Any row not reached (only possible via a cycle among "children" with
	// no unvisited root) is still shown, as an untitled chain of its own.
	for i := range rows {
		if !visited[i] {
			dfs(i, 0, &out)
		}
	}
	return out
}
