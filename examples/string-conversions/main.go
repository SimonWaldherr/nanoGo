// Run with Go or nanoGo to compare Unicode conversion and decimal formatting.
package main

import (
	"fmt"
	"strconv"
)

func main() {
	number := 65
	fmt.Println("decimal:", strconv.Itoa(number))
	fmt.Println("character:", string(rune(number)))
	fmt.Println("text:", string([]rune("Grüße 🌍")))
}
