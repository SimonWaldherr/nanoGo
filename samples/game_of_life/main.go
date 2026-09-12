// Conway's Life on a torus, evaluated entirely by nanoGo.
// Run: go run ./cmd/cli samples/game_of_life/main.go
package main

import "fmt"

// lifeStep advances packed rows. Each word holds bits cells (16 or 32).
// src and dst must be distinct, equally sized buffers of words*height ints.
// Width is words*bits; both dimensions wrap around the board.
func lifeStep(src []int, dst []int, words int, height int, bits int) {
	shift := bits - 1
	high := 1 << shift
	half := high - 1
	mask := half | high
	for y := 0; y < height; y++ {
		row := y * words
		above := row - words
		below := row + words
		if y == 0 {
			above = (height - 1) * words
		}
		if y == height-1 {
			below = 0
		}
		for x := 0; x < words; x++ {
			left := x - 1
			right := x + 1
			if x == 0 {
				left = words - 1
			}
			if x == words-1 {
				right = 0
			}
			{
				// Four values per scope fit nanoGo's unboxed integer locals.
				loA, hiA, loB, hiB := 0, 0, 0, 0
				{
					mid := src[above+x]
					a := (mid << 1) | ((src[above+left] >> shift) & 1)
					b := ((mid >> 1) & half) | ((src[above+right] & 1) << shift)
					low := a ^ b
					loA = low ^ mid
					hiA = (a & b) | (low & mid)
				}
				{
					mid := src[below+x]
					a := (mid << 1) | ((src[below+left] >> shift) & 1)
					b := ((mid >> 1) & half) | ((src[below+right] & 1) << shift)
					low := a ^ b
					loB = low ^ mid
					hiB = (a & b) | (low & mid)
				}
				{
					mid := src[row+x]
					a := (mid << 1) | ((src[row+left] >> shift) & 1)
					b := ((mid >> 1) & half) | ((src[row+right] & 1) << shift)
					low := loA ^ loB
					loB = (loA & loB) | (low & (a ^ b)) // carry from ones
					loA = low ^ a ^ b                   // ones
					low = hiA ^ hiB
					a = a & b
					b = a ^ loB
					hiA = (hiA & hiB) | (a & loB) | (low & b) // fours
					hiB = low ^ b                             // twos
					dst[row+x] = hiB &^ hiA & (loA | mid) & mask
				}
			}
		}
	}
}

func main() {
	words, height, bits := 3, 48, 32
	grid := make([]int, words*height)
	next := make([]int, words*height)
	// Deterministic seed for reproducible CLI runs and benchmarks.
	for y := 0; y < height; y++ {
		for x := 0; x < words*bits; x++ {
			if (x*17+y*31+x*y)%7 < 3 {
				grid[y*words+x/bits] |= 1 << (x % bits)
			}
		}
	}
	for generation := 0; generation < 100; generation++ {
		lifeStep(grid, next, words, height, bits)
		grid, next = next, grid
	}
	alive := 0
	for _, word := range grid {
		for bit := 0; bit < bits; bit++ {
			alive += (word >> bit) & 1
		}
	}
	fmt.Println("Life: 96x48, 100 generations, alive:", alive)
}
