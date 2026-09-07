package main

import (
	"fmt"
	"os"

	"github.com/ChinaKai/AHA2/internal/tray"
)

var version = "dev"

func main() {
	config, err := tray.ParseArgs(os.Args[1:], version)
	if err == nil {
		err = tray.Run(config)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
