package db

import (
	"reflect"
	"testing"
)

func TestPaginateSlice(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	tests := []struct {
		name          string
		offset, limit int
		want          []int
	}{
		{"no offset no limit", 0, 0, []int{1, 2, 3, 4, 5}},
		{"offset only", 2, 0, []int{3, 4, 5}},
		{"offset and limit", 1, 2, []int{2, 3}},
		{"limit larger than rest", 3, 10, []int{4, 5}},
		{"negative offset clamps to 0", -5, 2, []int{1, 2}},
		{"offset past end clamps to empty", 99, 0, []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paginateSlice(in, tt.offset, tt.limit)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("paginateSlice(%v, %d, %d) = %v, want %v", in, tt.offset, tt.limit, got, tt.want)
			}
		})
	}
}

func TestCostDescLess(t *testing.T) {
	tests := []struct {
		name         string
		costI, costJ float64
		timeI, timeJ int64
		want         bool
	}{
		{"higher cost first", 2, 1, 0, 0, true},
		{"lower cost later", 1, 2, 0, 0, false},
		{"cost tie breaks by recency", 1, 1, 20, 10, true},
		{"cost tie older loses", 1, 1, 10, 20, false},
		{"full tie is not less", 1, 1, 10, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := costDescLess(tt.costI, tt.costJ, tt.timeI, tt.timeJ); got != tt.want {
				t.Errorf("costDescLess(%v, %v, %v, %v) = %v, want %v",
					tt.costI, tt.costJ, tt.timeI, tt.timeJ, got, tt.want)
			}
		})
	}
}
