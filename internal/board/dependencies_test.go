package board

import "testing"

func mkRow(num int, status Status, ageSecs int, base, head string) MyPRRow {
	return MyPRRow{
		Status:  status,
		Number:  num,
		Repo:    "app",
		ageSecs: ageSecs,
		BaseRef: base,
		HeadRef: head,
	}
}

func numbers(rows []MyPRRow) []int {
	nums := make([]int, len(rows))
	for i, r := range rows {
		nums[i] = r.Number
	}
	return nums
}

func depths(rows []MyPRRow) []int {
	d := make([]int, len(rows))
	for i, r := range rows {
		d[i] = r.Depth
	}
	return d
}

func TestOrderWithDependencies_independentPRsSortByStatusThenAge(t *testing.T) {
	rows := []MyPRRow{
		mkRow(1, StatusInFlight, 100, "main", "a"),
		mkRow(2, StatusFailingCI, 50, "main", "b"),
		mkRow(3, StatusFailingCI, 200, "main", "c"),
	}
	got := orderWithDependencies(rows)
	// StatusFailingCI (priority 1) before StatusInFlight (priority 5); within
	// FailingCI, oldest (larger ageSecs) first: #3 (200s) before #2 (50s).
	if want := []int{3, 2, 1}; !equalInts(numbers(got), want) {
		t.Errorf("order = %v, want %v", numbers(got), want)
	}
	if want := []int{0, 0, 0}; !equalInts(depths(got), want) {
		t.Errorf("depths = %v, want %v", depths(got), want)
	}
}

func TestOrderWithDependencies_simpleChain(t *testing.T) {
	// #10 is the base; #11 stacks on #10; #12 stacks on #11.
	rows := []MyPRRow{
		mkRow(12, StatusInFlight, 10, "feature-11", "feature-12"),
		mkRow(10, StatusInFlight, 300, "main", "feature-10"),
		mkRow(11, StatusInFlight, 200, "feature-10", "feature-11"),
	}
	got := orderWithDependencies(rows)
	if want := []int{10, 11, 12}; !equalInts(numbers(got), want) {
		t.Errorf("order = %v, want %v", numbers(got), want)
	}
	if want := []int{0, 1, 2}; !equalInts(depths(got), want) {
		t.Errorf("depths = %v, want %v", depths(got), want)
	}
}

func TestOrderWithDependencies_fanOut(t *testing.T) {
	// #20 is the base for both #21 and #22.
	rows := []MyPRRow{
		mkRow(20, StatusInFlight, 300, "main", "feature-20"),
		mkRow(21, StatusFailingCI, 50, "feature-20", "feature-21"),
		mkRow(22, StatusInFlight, 100, "feature-20", "feature-22"),
	}
	got := orderWithDependencies(rows)
	// Root #20 first, then its children ordered by their own status+age:
	// #21 (FailingCI) before #22 (InFlight).
	if want := []int{20, 21, 22}; !equalInts(numbers(got), want) {
		t.Errorf("order = %v, want %v", numbers(got), want)
	}
	if want := []int{0, 1, 1}; !equalInts(depths(got), want) {
		t.Errorf("depths = %v, want %v", depths(got), want)
	}
}

func TestOrderWithDependencies_chainRankedAmongIndependents(t *testing.T) {
	// Chain root #30 is Draft (low priority); independent #31 is FailingCI
	// (high priority) and should rank before the whole chain.
	rows := []MyPRRow{
		mkRow(30, StatusDraft, 100, "main", "feature-30"),
		mkRow(32, StatusInFlight, 10, "feature-30", "feature-32"),
		mkRow(31, StatusFailingCI, 10, "main", "feature-31"),
	}
	got := orderWithDependencies(rows)
	if want := []int{31, 30, 32}; !equalInts(numbers(got), want) {
		t.Errorf("order = %v, want %v", numbers(got), want)
	}
	if want := []int{0, 0, 1}; !equalInts(depths(got), want) {
		t.Errorf("depths = %v, want %v", depths(got), want)
	}
}

func TestOrderWithDependencies_differentReposDontChain(t *testing.T) {
	rowA := mkRow(1, StatusInFlight, 100, "main", "shared-name")
	rowA.Repo = "app"
	rowB := mkRow(2, StatusInFlight, 50, "shared-name", "other")
	rowB.Repo = "zen" // same branch names, different repo: must not chain
	got := orderWithDependencies([]MyPRRow{rowA, rowB})
	for _, r := range got {
		if r.Depth != 0 {
			t.Errorf("PR #%d got depth %d, want 0 (cross-repo name collision must not chain)", r.Number, r.Depth)
		}
	}
}

func TestOrderWithDependencies_empty(t *testing.T) {
	got := orderWithDependencies(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
