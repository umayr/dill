// dill-agent is the standalone entrypoint for the read and staging agent.
package main

import (
	"os"

	"github.com/umayr/dill/internal/agentcmd"
)

func main() {
	os.Exit(agentcmd.Main(os.Args[1:], os.Stdout, os.Stderr, false))
}
