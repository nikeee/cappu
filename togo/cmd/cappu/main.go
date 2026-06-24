// Command cappu is the Go build's entry point. The CLI surface is declared as a
// kong struct (one field per subcommand); kong parses os.Args into it and
// generates --help / --version, replacing the hand-rolled parser and USAGE
// string the Node build (src/cli/main.ts) maintains by hand.
//
// Commands return an exit code via cli.RunX; a command's Run wraps that in
// cmdErr so the process exit code is preserved (0/1/2), which kong's plain
// error path would otherwise flatten.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/nikeee/cappu/internal/cli"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/dapserver"
	"github.com/nikeee/cappu/internal/lspserver"
	"github.com/nikeee/cappu/internal/mcp"
	"github.com/nikeee/cappu/internal/meta"
)

// CLI is the full command surface. Global flags sit at the top level (inherited
// by every command); each command is a struct field tagged cmd:"".
type CLI struct {
	Config  string           `short:"c" placeholder:"<file>" help:"Project config (default: ./cappu.json, JSONC)."`
	Version kong.VersionFlag `help:"Show the version"`

	Init         initCmd         `cmd:"" help:"Scaffold a project and write cappu.json"`
	ConfigSchema configSchemaCmd `cmd:"" name:"config-schema" help:"Print the JSON Schema for cappu.json"`
	Install      installCmd      `cmd:"" help:"Download the cappu.json dependencies"`
	Update       updateCmd       `cmd:"" help:"Bump declared dependencies to newest stable"`
	VersionCmd   versionCmd      `cmd:"" name:"version" help:"Bump the project version in cappu.json"`
	Verify       verifyCmd       `cmd:"" help:"Check installed jars against cappu-lock.json"`
	Audit        auditCmd        `cmd:"" help:"Scan resolved dependencies for vulnerabilities"`
	Licenses     licensesCmd     `cmd:"" help:"Print every dependency and its license"`
	Add          addCmd          `cmd:"" help:"Add dependencies and install them"`
	Remove       removeCmd       `cmd:"" help:"Remove dependencies and re-resolve"`
	Outdated     outdatedCmd     `cmd:"" help:"List dependencies with a newer published version"`
	Publish      publishCmd      `cmd:"" help:"Build the jar, generate its POM, and upload"`
	Search       searchCmd       `cmd:"" help:"Search the configured package sources"`
	Test         testCmd         `cmd:"" help:"Compile src/test/java and run JUnit"`
	SelfUpgrade  selfUpgradeCmd  `cmd:"" name:"self-upgrade" help:"Replace this binary with the latest CD build"`
	Rage         rageCmd         `cmd:"" help:"Open the issue tracker in your default browser"`
	Cache        cacheCmd        `cmd:"" help:"Manage the global download cache"`
	Lsp          lspCmd          `cmd:"" help:"Start the Java language server (JSON-RPC)"`
	Mcp          mcpCmd          `cmd:"" help:"Start the MCP server for agents (over stdio)"`
	Dap          dapCmd          `cmd:"" help:"Start the debug adapter (Debug Adapter Protocol over stdio)"`
	Compile      compileCmd      `cmd:"" help:"Compile .java files to .class bytecode"`
}

// --- exit-code plumbing ------------------------------------------------------

// cmdErr carries a process exit code out of a command's Run method.
type cmdErr int

func (c cmdErr) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

// exit turns a CLI exit code into the error kong's Run dispatch expects.
func exit(code int) error {
	if code == 0 {
		return nil
	}
	return cmdErr(code)
}

// appState lazily loads the project config so the pre-config commands (init,
// cache, self-upgrade, rage) never touch a possibly-broken cappu.json.
type appState struct {
	configPath string
	cfg        *config.Config
	loaded     bool
}

func (a *appState) config() (*config.Config, error) {
	if !a.loaded {
		wd, err := os.Getwd()
		if err != nil {
			wd = "."
		}
		cfg, err := config.Load(a.configPath, wd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			cli.EmitErrorAnnotation(err.Error())
			return nil, cmdErr(2)
		}
		a.cfg, a.loaded = cfg, true
	}
	return a.cfg, nil
}

// --- commands ----------------------------------------------------------------

type initCmd struct {
	Yes        bool `short:"y" help:"Take defaults"`
	WithSchema bool `name:"with-schema" help:"Also write cappu.schema.json"`
}

func (c *initCmd) Run(a *appState) error { return exit(cli.RunInit(a.configPath, c.WithSchema, c.Yes)) }

type installCmd struct {
	Verbose bool `short:"v" help:"List every installed jar"`
}

func (c *installCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunInstall(cfg, c.Verbose))
}

type updateCmd struct{}

func (c *updateCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunUpdate(a.configPath, cfg))
}

type versionCmd struct {
	Release string `arg:"" enum:"major,minor,patch" help:"major|minor|patch"`
}

func (c *versionCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunVersion(c.Release, a.configPath, cfg))
}

type verifyCmd struct{}

func (c *verifyCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunVerify(cfg))
}

type auditCmd struct {
	NoCache bool `name:"no-cache" help:"Ignore all caches (fresh scan)"`
	JSON    bool `name:"json" help:"Emit findings machine-readable"`
}

func (c *auditCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunAudit(cfg, c.NoCache, c.JSON || cli.AgentEnabled(os.Getenv)))
}

type licensesCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable"`
}

func (c *licensesCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunLicenses(cfg, c.JSON || cli.AgentEnabled(os.Getenv)))
}

type addCmd struct {
	Configuration string   `arg:"" enum:"api,implementation,annotationProcessor,testImplementation" help:"api|implementation|annotationProcessor|testImplementation"`
	Coordinates   []string `arg:"" name:"coord" help:"group:artifact[:version] ..."`
}

func (c *addCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunAdd(c.Configuration, c.Coordinates, a.configPath, cfg))
}

type removeCmd struct {
	Configuration string   `arg:"" enum:"api,implementation,annotationProcessor,testImplementation" help:"api|implementation|annotationProcessor|testImplementation"`
	Coordinates   []string `arg:"" name:"coord" help:"group:artifact ..."`
}

func (c *removeCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunRemove(c.Configuration, c.Coordinates, a.configPath, cfg))
}

type outdatedCmd struct{}

func (c *outdatedCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunOutdated(cfg))
}

type publishCmd struct {
	Repo string `placeholder:"<url>" help:"Target Maven registry"`
}

func (c *publishCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunPublish(cfg, c.Repo))
}

type searchCmd struct {
	Query []string `arg:"" optional:"" help:"Search terms"`
	JSON  bool     `name:"json" help:"Emit matches machine-readable"`
}

func (c *searchCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(c.Query, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "cappu: search needs a query, e.g. `cappu search gson`")
		return cmdErr(2)
	}
	return exit(cli.RunSearch(query, cfg, c.JSON || cli.AgentEnabled(os.Getenv)))
}

type testCmd struct{}

func (c *testCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunTest(cfg))
}

type selfUpgradeCmd struct{}

func (*selfUpgradeCmd) Run(*appState) error { return exit(cli.RunSelfUpgrade()) }

type rageCmd struct{}

func (*rageCmd) Run(*appState) error { return exit(cli.RunRage()) }

type configSchemaCmd struct{}

func (*configSchemaCmd) Run(*appState) error { return exit(cli.RunConfigSchema()) }

type cacheCmd struct {
	Clean cacheCleanCmd `cmd:"" help:"Remove the global download cache"`
}

type cacheCleanCmd struct{}

func (*cacheCleanCmd) Run(*appState) error { return exit(cli.RunCache([]string{"clean"})) }

type lspCmd struct {
	Port string `short:"p" placeholder:"<port>" help:"Listen on a TCP port instead of stdio"`
}

func (c *lspCmd) Run(a *appState) error {
	// Config is optional: a malformed/absent cappu.json simply means the server
	// runs with the JDK stub and any open documents only.
	cfg, err := a.config()
	if err != nil {
		cfg = nil
	}
	if c.Port != "" {
		ln, lerr := net.Listen("tcp", "127.0.0.1:"+c.Port)
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "cappu:", lerr)
			return exit(1)
		}
		conn, aerr := ln.Accept()
		if aerr != nil {
			fmt.Fprintln(os.Stderr, "cappu:", aerr)
			return exit(1)
		}
		if serr := lspserver.NewServer(cfg).Run(conn, conn); serr != nil {
			fmt.Fprintln(os.Stderr, "cappu:", serr)
			return exit(1)
		}
		return exit(0)
	}
	if serr := lspserver.Serve(cfg); serr != nil {
		fmt.Fprintln(os.Stderr, "cappu:", serr)
		return exit(1)
	}
	return exit(0)
}

// The MCP server (cli/mcp.ts, services/mcpServer.ts) exposes the Java semantic
// engine to agents over stdio. A project config is optional (the project tools
// need one; the semantic tools work with the JDK stub alone).
type mcpCmd struct{}

func (*mcpCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		cfg = nil
	}
	if serr := mcp.Serve(cfg); serr != nil {
		fmt.Fprintln(os.Stderr, "cappu:", serr)
		return exit(1)
	}
	return exit(0)
}

// The debug adapter (cli/dap.ts, services/dap/) compiles the project with debug
// info, launches its mainClass under JDWP, and bridges the Debug Adapter
// Protocol to JDWP over stdio (or --port TCP). Mirrors lspCmd.
type dapCmd struct {
	Port string `short:"p" placeholder:"<port>" help:"Listen on a TCP port instead of stdio"`
}

func (c *dapCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		cfg = nil
	}
	if c.Port != "" {
		ln, lerr := net.Listen("tcp", "127.0.0.1:"+c.Port)
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "cappu:", lerr)
			return exit(1)
		}
		conn, aerr := ln.Accept()
		if aerr != nil {
			fmt.Fprintln(os.Stderr, "cappu:", aerr)
			return exit(1)
		}
		if serr := dapserver.Run(cfg, conn, conn); serr != nil {
			fmt.Fprintln(os.Stderr, "cappu:", serr)
			return exit(1)
		}
		return exit(0)
	}
	if serr := dapserver.Serve(cfg); serr != nil {
		fmt.Fprintln(os.Stderr, "cappu:", serr)
		return exit(1)
	}
	return exit(0)
}

type compileCmd struct {
	Output   string   `short:"o" placeholder:"<kind>" help:"classes | jar | fat-jar"`
	Artifact string   `placeholder:"<name>" help:"Jar base name in ./dist"`
	Quiet    bool     `short:"q" help:"Do not print each emitted .class file"`
	Files    []string `arg:"" optional:"" help:"Specific .java files to compile"`
}

func (c *compileCmd) Run(a *appState) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	return exit(cli.RunCompile(c.Files, c.Output, c.Artifact, c.Quiet, cfg))
}

func main() {
	var root CLI
	ctx := kong.Parse(&root,
		kong.Name("cappu"),
		kong.Description("The Java language server, package manager and build toolchain of your dreams."),
		kong.UsageOnError(),
		kong.Vars{"version": meta.Version},
	)
	app := &appState{configPath: root.Config}
	start := time.Now()
	err := ctx.Run(app)
	// Print how long the dependency/build commands took, however they exit.
	cli.PrintDurationFooter(ctx.Command(), time.Since(start))
	var ce cmdErr
	if errors.As(err, &ce) {
		os.Exit(int(ce))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		os.Exit(1)
	}
}
