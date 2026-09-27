package main

import "fmt"

var (
	version         = "dev"
	commit          = "none"
	protocolVersion = "0"
)

func main() {
	fmt.Printf("lanchat %s (commit %s, protocol %s)\n", version, commit, protocolVersion)
}
