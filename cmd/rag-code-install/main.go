// Command rag-code-install installs RagCode MCP from the directory it runs
// in, or removes it with -uninstall.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/doITmagic/rag-code-mcp/v2/internal/install"
	"github.com/doITmagic/rag-code-mcp/v2/internal/uninstall"
)

func main() {
	yes := flag.Bool("y", false, "answer yes to every prompt")
	skipDownload := flag.Bool("skip-download", false, "do not download the embedding runtime and model now; the server fetches them on first use")
	skipIDE := flag.Bool("skip-ide", false, "do not write IDE MCP configs")
	un := flag.Bool("uninstall", false, "remove RagCode MCP instead of installing it")
	docker := flag.Bool("docker", false, "with -uninstall: also remove the ragcode-ollama and ragcode-qdrant containers of earlier versions")
	flag.Parse()
	// Parsing stops at the first non-flag argument, so `rag-code-install x -uninstall` would install.
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q (flags only; see -h)\n", flag.Arg(0))
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	if *un {
		err = uninstall.Run(ctx, uninstall.Options{Yes: *yes, Docker: *docker})
	} else {
		err = install.Run(ctx, install.Options{Yes: *yes, SkipDownload: *skipDownload, SkipIDE: *skipIDE})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
