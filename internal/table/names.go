package table

import (
	"cmp"
	"strings"
)

// natural compares names as people read the numbers in them, and case
// aside: "Meja 2" before "Meja 10", "a1" with "A1". A run of digits
// compares by its value, leading zeros aside.
func natural(a, b string) int {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if digit(ra[i]) && digit(rb[j]) {
			si, sj := i, j
			for i < len(ra) && digit(ra[i]) {
				i++
			}
			for j < len(rb) && digit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			// Without leading zeros, the longer run is the larger number.
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb)); c != 0 {
				return c
			}
			continue
		}
		if c := cmp.Compare(ra[i], rb[j]); c != 0 {
			return c
		}
		i, j = i+1, j+1
	}
	return cmp.Compare(len(ra)-i, len(rb)-j)
}

func digit(r rune) bool { return r >= '0' && r <= '9' }
