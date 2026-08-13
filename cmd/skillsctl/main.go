// Command skillsctl installs, updates and removes agent skills.
package main

import (
	"os"

	"github.com/richardcase/skillsctl/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
