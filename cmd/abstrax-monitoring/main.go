package main

import (
	"os"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/commands"
)

func main() {
	os.Exit(commands.Execute())
}
