// Command cappu is the Go build's unified entry point. It parses arguments and
// dispatches each subcommand to internal/cli (one function per command),
// mirroring src/cli/main.ts. Commands not yet ported dispatch to cli.Stub.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nikeee/cappu/internal/cli"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/meta"
)

const usageTemplate = `cappu %s

Usage:
  cappu init [-y] [--with-schema]    Scaffold a project and write cappu.json
  cappu install [-v]                 Download the cappu.json dependencies
  cappu update                       Bump declared dependencies to newest stable
  cappu version <major|minor|patch>  Bump the project version in cappu.json
  cappu verify                       Check installed jars against cappu-lock.json
  cappu audit [--no-cache] [--json]  Scan resolved dependencies for vulnerabilities
  cappu licenses [--json]            Print every dependency and its license
  cappu add <configuration> <coord...>  Add dependencies and install them
  cappu publish [--repo <url>]       Build the jar, generate its POM, and upload
  cappu search <query>               Search the configured package sources
  cappu test                         Compile src/test/java and run JUnit
  cappu self-upgrade                 Replace this binary with the latest CD build
  cappu rage                         Open the issue tracker in your default browser
  cappu cache clean                  Remove the global download cache
  cappu lsp [options]                Start the Java language server (JSON-RPC)
  cappu compile [options] [file...]  Compile .java files to .class bytecode

Options:
  -c, --config <file>   Project config (default: ./cappu.json, JSONC).

Global:
  -h, --help            Show this help
      --version         Show the version
`

// boolFlags are the options that take no value (everything else is a string).
var boolFlags = map[string]bool{
	"quiet": true, "verbose": true, "with-schema": true, "yes": true,
	"json": true, "no-cache": true, "help": true, "version": true,
}

// shortFlags maps single-letter flags to their long names.
var shortFlags = map[string]string{
	"c": "config", "p": "port", "o": "output", "q": "quiet",
	"v": "verbose", "y": "yes", "h": "help",
}

// parseArgs splits argv into flag values and positionals, allowing positionals
// and flags to interleave (like Node's util.parseArgs). It errors on an unknown
// flag or a missing value, mirroring main.ts's friendly error path.
func parseArgs(argv []string) (map[string]string, []string, error) {
	values := map[string]string{}
	var positionals []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case strings.HasPrefix(arg, "--"):
			name, val, hasVal := strings.Cut(arg[2:], "=")
			if err := assign(values, argv, &i, name, val, hasVal); err != nil {
				return nil, nil, err
			}
		case strings.HasPrefix(arg, "-") && arg != "-":
			short := arg[1:]
			name, ok := shortFlags[short]
			if !ok {
				return nil, nil, fmt.Errorf("Unknown option '-%s'", short)
			}
			if err := assign(values, argv, &i, name, "", false); err != nil {
				return nil, nil, err
			}
		default:
			positionals = append(positionals, arg)
		}
	}
	return values, positionals, nil
}

func assign(values map[string]string, argv []string, i *int, name, val string, hasVal bool) error {
	if name != "config" && name != "port" && name != "output" && name != "artifact" &&
		name != "repo" && !boolFlags[name] {
		return fmt.Errorf("Unknown option '--%s'", name)
	}
	if boolFlags[name] {
		values[name] = "true"
		return nil
	}
	if hasVal {
		values[name] = val
		return nil
	}
	if *i+1 >= len(argv) {
		return fmt.Errorf("Option '--%s' argument missing", name)
	}
	*i++
	values[name] = argv[*i]
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	values, positionals, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\nRun `cappu --help` for usage.\n", err)
		return 2
	}

	if values["version"] != "" {
		fmt.Fprintf(os.Stdout, "%s\n", meta.Version)
		return 0
	}
	var command string
	var files []string
	if len(positionals) > 0 {
		command, files = positionals[0], positionals[1:]
	}
	if values["help"] != "" || command == "" {
		fmt.Fprintf(os.Stdout, usageTemplate, meta.Version)
		if values["help"] != "" {
			return 0
		}
		return 2
	}

	// init, cache, self-upgrade and rage run before loadConfig: none depends on
	// (nor should be blocked by) an existing, possibly broken project config.
	switch command {
	case "init":
		return cli.Stub("init")
	case "cache":
		return cli.RunCache(files)
	case "self-upgrade":
		return cli.Stub("self-upgrade")
	case "rage":
		return cli.RunRage()
	}

	cfg, err := config.Load(values["config"], mustGetwd())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 2
	}

	switch command {
	case "version":
		return cli.RunVersion(first(files), values["config"], cfg)
	case "verify":
		return cli.RunVerify(cfg)
	case "search":
		query := strings.TrimSpace(strings.Join(files, " "))
		if query == "" {
			fmt.Fprintln(os.Stderr, "cappu: search needs a query, e.g. `cappu search gson`")
			return 2
		}
		return cli.RunSearch(query, cfg)
	case "licenses":
		return cli.RunLicenses(cfg, values["json"] != "")
	case "add", "install", "update", "audit", "publish", "lsp", "test", "compile":
		return cli.Stub(command)
	default:
		fmt.Fprintf(os.Stderr, "cappu: unknown command '%s'\n\n", command)
		fmt.Fprintf(os.Stderr, usageTemplate, meta.Version)
		return 2
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
