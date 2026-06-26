// A deliberately buggy example program for sl-dbg integration tests.
//
// Run with sl-dbg:
//
//	sl-dbg start --lang go --program ./examples/go/buggy.go --stop-on-entry
//	sl-dbg break ./examples/go/buggy.go:22 --if "item < 0"
//	sl-dbg continue
//	sl-dbg locals
package main

import "fmt"

func compute(item int) int {
	// Bug: panics on item == 0; silently negative on item < 0.
	if item == 0 {
		return 0
	}
	return 1000 / item
}

func process(items []int) int {
	total := 0
	for _, item := range items { // line 22
		x := compute(item)   // line 23
		total += x           // line 24
	}
	return total
}

func main() {
	data := []int{10, 5, 2, 0, -1, 4}
	result := process(data)
	fmt.Printf("result=%d\n", result)
}
