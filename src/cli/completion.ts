// `cappu completion <bash|zsh>`: print a shell completion script to stdout.
// The scripts are static (they never call back into cappu): commands, per-command
// flags, flag values with a fixed set (--output, --format), the first argument
// of add/remove/version/cache/completion, and .java/.class file arguments.
//
// The Go build embeds byte-identical copies under togo/internal/cli/completion/;
// regenerate them with `node --run completion:write` after editing these.

const COMMANDS: [name: string, desc: string][] = [
  ["init", "Scaffold a project and write cappu.json"],
  ["config-schema", "Print the JSON Schema for cappu.json"],
  ["install", "Download the cappu.json dependencies"],
  ["update", "Bump declared dependencies to newest stable"],
  ["outdated", "List dependencies with a newer published version"],
  ["tree", "Print the resolved dependency graph as a tree"],
  ["add", "Add dependencies and install them"],
  ["remove", "Remove dependencies and re-resolve"],
  ["audit", "Scan resolved dependencies for vulnerabilities"],
  ["licenses", "Print every dependency and its license"],
  ["verify", "Check installed jars against cappu-lock.json"],
  ["search", "Search the configured package sources"],
  ["show", "Show a detail card for one package"],
  ["publish", "Build the jar, generate its POM, and upload"],
  ["compile", "Compile .java files to .class bytecode"],
  ["check", "Type-check without writing class files"],
  ["decompile", "Reconstruct Java source from .class files"],
  ["format", "Check or rewrite Java formatting"],
  ["run", "Compile the project and run it on the JVM"],
  ["test", "Compile src/test/java and run JUnit"],
  ["lsp", "Start the Java language server"],
  ["dap", "Start the debug adapter"],
  ["mcp", "Start the MCP server for agents"],
  ["version", "Bump the project version in cappu.json"],
  ["self-upgrade", "Replace this binary with the latest CD build"],
  ["rage", "Print version/environment info for bug reports"],
  ["cache", "Manage the global download cache"],
  ["completion", "Print a shell completion script"],
];

const CONFIGURATIONS = "api implementation annotationProcessor testImplementation";

export const BASH_COMPLETION = `# bash completion for cappu
# Load it with: source <(cappu completion bash)

_cappu_files() {
  # $1: a compgen -X filter (e.g. '!*.java'), or empty for any file
  local f
  COMPREPLY=()
  while IFS= read -r f; do
    COMPREPLY+=("$f")
  done < <(compgen -f -X "$1" -- "$cur"; [[ -n $1 ]] && compgen -d -- "$cur")
  compopt -o filenames 2>/dev/null
}

_cappu() {
  local cur=\${COMP_WORDS[COMP_CWORD]} prev=\${COMP_WORDS[COMP_CWORD-1]}
  local cmd= args=0 i w

  for ((i = 1; i < COMP_CWORD; i++)); do
    w=\${COMP_WORDS[i]}
    case $w in
      -c|--config|-o|--output|-p|--port|--format|--repo|--artifact) ((i++)) ;;
      -*) ;;
      *) if [[ -z $cmd ]]; then cmd=$w; else ((args++)); fi ;;
    esac
  done

  case $prev in
    -c|--config) _cappu_files ''; return ;;
    -o|--output) COMPREPLY=($(compgen -W "classes jar fat-jar" -- "$cur")); return ;;
    --format) COMPREPLY=($(compgen -W "text sarif" -- "$cur")); return ;;
    -p|--port|--repo|--artifact) return ;;
  esac

  if [[ -z $cmd ]]; then
    if [[ $cur == -* ]]; then
      COMPREPLY=($(compgen -W "-c --config -h --help --version" -- "$cur"))
    else
      COMPREPLY=($(compgen -W "${COMMANDS.map(([name]) => name).join(" ")}" -- "$cur"))
    fi
    return
  fi

  local flags= first= files=
  case $cmd in
    init) flags="-y --yes --with-schema" ;;
    install) flags="-v --verbose --locked" ;;
    tree|licenses|search|show) flags="--json" ;;
    audit) flags="--no-cache --format" ;;
    publish) flags="--repo" ;;
    compile) flags="-o --output --artifact -q --quiet"; files='!*.java' ;;
    check) files='!*.java' ;;
    format) flags="-w --write"; files='!*.java' ;;
    decompile) flags="--disasm"; files='!*.class' ;;
    lsp|dap) flags="-p --port" ;;
    rage) flags="--open" ;;
    add|remove) first="${CONFIGURATIONS}" ;;
    version) first="major minor patch" ;;
    cache) first="clean verify" ;;
    completion) first="bash zsh" ;;
  esac

  if [[ $cur == -* ]]; then
    COMPREPLY=($(compgen -W "$flags -c --config -h --help" -- "$cur"))
  elif [[ -n $first ]]; then
    ((args == 0)) && COMPREPLY=($(compgen -W "$first" -- "$cur"))
  elif [[ -n $files ]]; then
    _cappu_files "$files"
  fi
}

complete -F _cappu cappu
`;

export const ZSH_COMPLETION = `#compdef cappu
# zsh completion for cappu
# Load it with: source <(cappu completion zsh)
# or save it as _cappu in a directory on $fpath.

_cappu() {
  local curcontext=$curcontext state line ret=1
  local -a commands common
  commands=(
${COMMANDS.map(([name, desc]) => `    '${name}:${desc}'`).join("\n")}
  )
  common=(
    '(-c --config)'{-c,--config}'[project config]:config file:_files'
    '(- *)'{-h,--help}'[show help]'
  )

  _arguments -C $common \\
    '(- *)--version[show the version]' \\
    '1: :->command' \\
    '*:: :->args' && ret=0

  case $state in
    command)
      _describe -t commands 'cappu command' commands && ret=0
      ;;
    args)
      curcontext=\${curcontext%:*:*}:cappu-$words[1]:
      case $words[1] in
        init)
          _arguments $common \\
            '(-y --yes)'{-y,--yes}'[take defaults]' \\
            '--with-schema[also write cappu.schema.json]' && ret=0 ;;
        install)
          _arguments $common \\
            '(-v --verbose)'{-v,--verbose}'[list every installed jar]' \\
            '--locked[fail if cappu-lock.json is stale or missing]' && ret=0 ;;
        tree|licenses)
          _arguments $common '--json[emit machine-readable]' && ret=0 ;;
        search)
          _arguments $common '--json[emit machine-readable]' '*:query: ' && ret=0 ;;
        show)
          _arguments $common '--json[emit machine-readable]' '1:package (group\\:artifact[\\:version]): ' && ret=0 ;;
        add|remove)
          _arguments $common \\
            '1:configuration:(${CONFIGURATIONS})' \\
            '*:coordinate (group\\:artifact[\\:version]): ' && ret=0 ;;
        audit)
          _arguments $common \\
            '--no-cache[ignore all caches]' \\
            '--format[output format]:format:(text sarif)' && ret=0 ;;
        publish)
          _arguments $common '--repo[target Maven registry]:url: ' && ret=0 ;;
        compile)
          _arguments $common \\
            '(-o --output)'{-o,--output}'[what to produce in ./dist]:kind:(classes jar fat-jar)' \\
            '--artifact[jar base name in ./dist]:name: ' \\
            '(-q --quiet)'{-q,--quiet}'[do not print each emitted .class file]' \\
            '*:java file:_files -g "*.java"' && ret=0 ;;
        check)
          _arguments $common '*:java file:_files -g "*.java"' && ret=0 ;;
        format)
          _arguments $common \\
            '(-w --write)'{-w,--write}'[rewrite the files in place]' \\
            '*:java file:_files -g "*.java"' && ret=0 ;;
        decompile)
          _arguments $common \\
            '--disasm[print the bytecode in javap -c -p layout]' \\
            '*:class file:_files -g "*.class"' && ret=0 ;;
        lsp|dap)
          _arguments $common '(-p --port)'{-p,--port}'[listen on a TCP port]:port: ' && ret=0 ;;
        rage)
          _arguments $common '--open[open the issue tracker in your browser]' && ret=0 ;;
        version)
          _arguments $common '1:release:(major minor patch)' && ret=0 ;;
        cache)
          _arguments $common '1:subcommand:((clean\\:"remove the global download cache" verify\\:"check cached artifacts against their hashes"))' && ret=0 ;;
        completion)
          _arguments $common '1:shell:(bash zsh)' && ret=0 ;;
        run)
          _arguments $common '*:program argument:_default' && ret=0 ;;
        *)
          _arguments $common && ret=0 ;;
      esac
      ;;
  esac
  return ret
}

if [[ $funcstack[1] == _cappu ]]; then
  _cappu "$@"
else
  compdef _cappu cappu
fi
`;

export function runCompletion(shell: string | undefined): never {
  if (shell === "bash" || shell === "zsh") {
    process.stdout.write(shell === "bash" ? BASH_COMPLETION : ZSH_COMPLETION);
    process.exit(0);
  }
  process.stderr.write(
    shell === undefined
      ? "cappu: completion needs a shell: bash or zsh\n"
      : `cappu: unknown shell '${shell}' (expected: bash, zsh)\n`,
  );
  process.exit(2);
}
