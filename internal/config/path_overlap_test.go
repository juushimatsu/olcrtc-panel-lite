package config

import "testing"

func TestPathsOverlapDetection(t *testing.T) {
	cases := []struct {
		left, right string
		want        bool
	}{
		{"/panel", "/panel/sub", true},
		{"/panel/sub", "/panel", true},
		{"/panel", "/other", false},
		{"/a", "/a", true},
		{"/sub", "/subscription", false},
		{"/a/b", "/a/b/c", true},
		{"/a/bc", "/a/b", false},
	}
	for _, tc := range cases {
		got := pathsOverlap(tc.left, tc.right)
		if got != tc.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}
