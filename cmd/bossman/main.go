package main

import (
	"os"

	"github.com/Hoodoo/bossman/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
