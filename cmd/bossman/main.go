package main

import (
	"os"

	"bossman/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
