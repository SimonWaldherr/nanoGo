// This program runs unchanged with standard Go and nanoGo.
package main

import (
	"encoding/json"
	"fmt"
)

type Sample struct {
	Name   string      `json:"name"`
	Points [][]float64 `json:"points"`
	Bytes  []byte      `json:"bytes"`
}

func main() {
	input := Sample{Name: "example", Points: [][]float64{{0, 0}, {1, 1}}, Bytes: []byte("Go")}
	data, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
	var output Sample
	if err := json.Unmarshal(data, &output); err != nil {
		panic(err)
	}
	fmt.Println(output.Name, output.Points[1][1], string(output.Bytes))
	if err := json.Unmarshal([]byte("{"), &output); err != nil {
		fmt.Println("invalid JSON rejected")
	}
}
