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
	ollama := flag.String("ollama", "docker", "Ollama: docker (run a container), local (use the one running) or skip")
	gpu := flag.Bool("gpu", true, "try GPU for the Ollama container, fall back to CPU")
	yes := flag.Bool("y", false, "answer yes to every prompt")
	modelsDir := flag.String("models-dir", "", "host directory mounted as the Ollama model store (default: a Docker volume)")
	skipIDE := flag.Bool("skip-ide", false, "do not write IDE MCP configs")
	un := flag.Bool("uninstall", false, "remove RagCode MCP instead of installing it")
	docker := flag.Bool("docker", false, "with -uninstall: also remove the ragcode-ollama container (and a leftover ragcode-qdrant)")
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
		err = install.Run(ctx, install.Options{
			Ollama: *ollama, GPU: *gpu, Yes: *yes, ModelsDir: *modelsDir, SkipIDE: *skipIDE,
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
