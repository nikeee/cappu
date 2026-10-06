#compdef cappu
# zsh completion for cappu
# Load it with: source <(cappu completion zsh)
# or save it as _cappu in a directory on $fpath.

_cappu() {
  local curcontext=$curcontext state line ret=1
  local -a commands common
  commands=(
    'init:Scaffold a project and write cappu.json'
    'config-schema:Print the JSON Schema for cappu.json'
    'install:Download the cappu.json dependencies'
    'update:Bump declared dependencies to newest stable'
    'outdated:List dependencies with a newer published version'
    'tree:Print the resolved dependency graph as a tree'
    'add:Add dependencies and install them'
    'remove:Remove dependencies and re-resolve'
    'audit:Scan resolved dependencies for vulnerabilities'
    'licenses:Print every dependency and its license'
    'verify:Check installed jars against cappu-lock.json'
    'search:Search the configured package sources'
    'show:Show a detail card for one package'
    'publish:Build the jar, generate its POM, and upload'
    'compile:Compile .java files to .class bytecode'
    'check:Type-check without writing class files'
    'decompile:Reconstruct Java source from .class files'
    'format:Check or rewrite Java formatting'
    'run:Compile the project and run it on the JVM'
    'test:Compile src/test/java and run JUnit'
    'lsp:Start the Java language server'
    'dap:Start the debug adapter'
    'mcp:Start the MCP server for agents'
    'version:Bump the project version in cappu.json'
    'self-upgrade:Replace this binary with the latest CD build'
    'rage:Print version/environment info for bug reports'
    'cache:Manage the global download cache'
    'completion:Print a shell completion script'
  )
  common=(
    '(-c --config)'{-c,--config}'[project config]:config file:_files'
    '(- *)'{-h,--help}'[show help]'
  )

  _arguments -C $common \
    '(- *)--version[show the version]' \
    '1: :->command' \
    '*:: :->args' && ret=0

  case $state in
    command)
      _describe -t commands 'cappu command' commands && ret=0
      ;;
    args)
      curcontext=${curcontext%:*:*}:cappu-$words[1]:
      case $words[1] in
        init)
          _arguments $common \
            '(-y --yes)'{-y,--yes}'[take defaults]' \
            '--with-schema[also write cappu.schema.json]' && ret=0 ;;
        install)
          _arguments $common \
            '(-v --verbose)'{-v,--verbose}'[list every installed jar]' \
            '--locked[fail if cappu-lock.json is stale or missing]' && ret=0 ;;
        tree|licenses)
          _arguments $common '--json[emit machine-readable]' && ret=0 ;;
        search)
          _arguments $common '--json[emit machine-readable]' '*:query: ' && ret=0 ;;
        show)
          _arguments $common '--json[emit machine-readable]' '1:package (group\:artifact[\:version]): ' && ret=0 ;;
        add|remove)
          _arguments $common \
            '1:configuration:(api implementation annotationProcessor testImplementation)' \
            '*:coordinate (group\:artifact[\:version]): ' && ret=0 ;;
        audit)
          _arguments $common \
            '--no-cache[ignore all caches]' \
            '--format[output format]:format:(text sarif)' && ret=0 ;;
        publish)
          _arguments $common '--repo[target Maven registry]:url: ' && ret=0 ;;
        compile)
          _arguments $common \
            '(-o --output)'{-o,--output}'[what to produce in ./dist]:kind:(classes jar fat-jar)' \
            '--artifact[jar base name in ./dist]:name: ' \
            '(-q --quiet)'{-q,--quiet}'[do not print each emitted .class file]' \
            '*:java file:_files -g "*.java"' && ret=0 ;;
        check)
          _arguments $common '*:java file:_files -g "*.java"' && ret=0 ;;
        format)
          _arguments $common \
            '(-w --write)'{-w,--write}'[rewrite the files in place]' \
            '*:java file:_files -g "*.java"' && ret=0 ;;
        decompile)
          _arguments $common \
            '--disasm[print the bytecode in javap -c -p layout]' \
            '*:class file:_files -g "*.class"' && ret=0 ;;
        lsp|dap)
          _arguments $common '(-p --port)'{-p,--port}'[listen on a TCP port]:port: ' && ret=0 ;;
        rage)
          _arguments $common '--open[open the issue tracker in your browser]' && ret=0 ;;
        version)
          _arguments $common '1:release:(major minor patch)' && ret=0 ;;
        cache)
          _arguments $common '1:subcommand:((clean\:"remove the global download cache" verify\:"check cached artifacts against their hashes"))' && ret=0 ;;
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
